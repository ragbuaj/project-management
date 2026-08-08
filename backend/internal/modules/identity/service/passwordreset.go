package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ragbuaj/project-management/backend/internal/mail"
	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
)

// Limiter counts one hit against key and reports whether it is under the limit.
//
// It is declared here, at the consumer, rather than beside its implementation
// (ADR-0008), and it is deliberately not FailureCounter: what ADR-0010 bounds on
// this path is **requests**, not failures. Asking for a reset never fails in the
// sense a login does — an address with no account is answered exactly like one
// with an account — so there would be nothing to count.
type Limiter interface {
	Allow(ctx context.Context, key string) (allowed bool, retryAfter time.Duration, err error)
}

// ErrPasswordResetNotUsable is the one answer to a link that was never issued,
// one that has expired, one that has already set a password, and one that is not
// shaped like a token at all.
//
// They are four different facts and the caller must not be able to tell them
// apart. The reason is not quite the invitation's: whoever holds this link
// learns nothing about which addresses have accounts either way. It is that a
// client which could tell "expired" from "never issued" could tell a token it
// guessed wrong from one it guessed right and was merely too late on. The
// difference belongs in the log.
var ErrPasswordResetNotUsable = errors.New("this password reset link cannot be used")

// PasswordResets sends the link somebody who has forgotten their password uses
// to choose a new one.
type PasswordResets struct {
	inTx   InTx
	sender Sender
	// perAccount is the 3-per-hour bucket of ADR-0010, keyed by the address that
	// was submitted. It is checked in here rather than as middleware for a
	// mechanical reason: the address is in the request body, and a middleware
	// that read the body to find it would consume it.
	perAccount Limiter
	// baseURL is where the link points. It comes from APP_BASE_URL rather than
	// from the request, because a Host header is attacker-controlled and this
	// link replaces a password.
	baseURL *url.URL
	log     *slog.Logger
	now     func() time.Time
}

// NewPasswordResets wires the service.
func NewPasswordResets(inTx InTx, sender Sender, perAccount Limiter, baseURL *url.URL, log *slog.Logger, now func() time.Time) *PasswordResets {
	return &PasswordResets{
		inTx:       inTx,
		sender:     sender,
		perAccount: perAccount,
		baseURL:    baseURL,
		log:        log,
		now:        now,
	}
}

// Request records a reset and sends the link, if there is an account behind the
// address.
//
// **It answers the same way whether there is one or not.** docs/threat-model.md
// closes account enumeration on this path, and an endpoint that reported "no
// such address" would be an address checker that anybody can call without
// signing in. Which of the two it was goes to the log.
//
// The residual is the clock: an address with an account costs one SMTP round
// trip and one without costs nothing, so the two are not indistinguishable to a
// stopwatch. Closing that needs the send off the request path, which needs the
// outbox (ADR-0002) to have a consumer — Langkah 24. It is accepted and written
// down rather than papered over with a goroutine that would lose messages on
// every deploy.
//
// retryAfter is only meaningful together with ErrTooManyAttempts, which is the
// same shape LoginGuard.Check answers in.
//
// The message is sent after the transaction commits, for the reason spelled out
// on Invitations.Create: an SMTP round trip inside a transaction is a lock on
// password_resets held for as long as the mail server feels like taking.
func (s *PasswordResets) Request(ctx context.Context, email string) (retryAfter time.Duration, err error) {
	address, err := normalizeEmail(email)
	if err != nil {
		// Before the bucket, so a malformed address cannot spend somebody's
		// allowance. It is also the one refusal on this endpoint that says
		// something, and it says nothing about whether an account exists.
		return 0, err
	}

	if wait, err := s.allowed(ctx, address); err != nil {
		return wait, err
	}

	token, digest, err := identitydom.NewPasswordResetToken()
	if err != nil {
		return 0, fmt.Errorf("new password reset token: %w", err)
	}

	var sendTo string

	err = s.inTx(ctx, func(store TxStore) error {
		user, err := store.GetUserByEmail(ctx, address)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Not an error. The caller is told what everybody is told, and
				// the log is where somebody chasing "I never got the mail" finds
				// out that there was nothing to send. The address is not logged
				// (rules/45-privacy); the request id already ties this line to
				// the request that carried it.
				s.log.InfoContext(ctx, "password reset requested for an address with no account")

				return nil
			}

			return fmt.Errorf("look up account: %w", err)
		}

		// Any link already sent to this account stops working here, so that
		// clicking "forgot password" four times does not leave four ways in.
		if _, err := store.ExpireOpenPasswordResetsForUser(ctx, user.ID); err != nil {
			return fmt.Errorf("supersede open resets: %w", err)
		}

		if _, err := store.CreatePasswordReset(ctx, identityrepo.CreatePasswordResetParams{
			UserID:    user.ID,
			TokenHash: digest,
			ExpiresAt: pgtype.Timestamptz{Time: identitydom.NewPasswordResetExpiry(s.now()), Valid: true},
		}); err != nil {
			return fmt.Errorf("create password reset: %w", err)
		}

		// The address as the account holds it, not as it was typed. They differ
		// only in case, and the one the account holds is the one somebody chose
		// for themselves.
		sendTo = user.Email

		return nil
	})
	if err != nil {
		return 0, err
	}

	if sendTo == "" {
		return 0, nil
	}

	if err := s.sender.Send(ctx, s.message(sendTo, token)); err != nil {
		// Swallowed into the log, and this is the deliberate opposite of what
		// Invitations.Create does with the same failure. There it becomes a 500,
		// because only the owner sees it and an invitation nobody receives is not
		// an invitation. Here a 500 would arrive **only when the address has an
		// account**, which hands back exactly the enumeration the constant
		// answer above exists to deny.
		//
		// The row stays and is inert: the token was never stored, so nothing can
		// reach it, and it expires within the hour. Asking again supersedes it.
		s.log.ErrorContext(ctx, "password reset recorded but not delivered",
			slog.String("error", err.Error()))
	}

	return 0, nil
}

// Confirm replaces the password the link belongs to and signs every device out.
//
// The stamp on the reset, the new hash, and the deletion of the sessions are one
// transaction. A confirmation that loses the race stamps no row, returns before
// the hash is written, and commits nothing — which matters more here than it
// does for an invitation, because the loser would otherwise be setting the
// password of an account that is already somebody's.
//
// Every session goes, including the ones belonging to whoever is doing this.
// Somebody resetting a password after a scare is trying to get an account out of
// another pair of hands, and a reset that left the old sessions alive would
// leave it exactly where it was. It is also why this does not hand back a
// session of its own: the caller signs in afterwards with the password they just
// chose (keputusan pemilik, 2026-08-08), which is the deliberate difference from
// redeeming an invitation.
//
// The password is hashed before the transaction opens. Argon2id is expensive on
// purpose — 19 MiB and two passes, ADR-0005 — and a transaction held open across
// it is a row lock held for the length of a deliberately slow computation.
func (s *PasswordResets) Confirm(ctx context.Context, token, password string) error {
	digest, err := identitydom.PasswordResetTokenDigest(token)
	if err != nil {
		// A link that is not shaped like one is answered exactly as an unknown
		// link, and the shape is the only thing that has been learned.
		s.log.InfoContext(ctx, "password reset refused",
			slog.String("reason", "the token is malformed"))

		return ErrPasswordResetNotUsable
	}

	// Whatever ADR-0009 rules on the password reaches the caller as it is: it is
	// something they can still fix, and a form that refuses without saying why is
	// a form nobody gets past. The breach blocklist joins this call in piece h.
	hash, err := identitydom.HashPassword(password)
	if err != nil {
		return err
	}

	return s.inTx(ctx, func(store TxStore) error {
		row, err := store.GetPasswordResetByTokenHash(ctx, digest)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				s.log.InfoContext(ctx, "password reset refused",
					slog.String("reason", "no reset carries this token"))

				return ErrPasswordResetNotUsable
			}

			return fmt.Errorf("get password reset: %w", err)
		}

		reset := identitydom.PasswordReset{
			ID:        row.ID,
			UserID:    row.UserID,
			UsedAt:    timestamp(row.UsedAt),
			CreatedAt: row.CreatedAt.Time,
			ExpiresAt: row.ExpiresAt.Time,
		}

		if !reset.IsUsable(s.now()) {
			// Here is where the facts the client is not told apart are kept
			// apart, so that somebody debugging a link an employee cannot use can
			// find out which of them it was.
			s.log.InfoContext(ctx, "password reset refused",
				slog.String("password_reset_id", reset.ID),
				slog.Bool("already_used", reset.IsUsed()),
				slog.Bool("expired", reset.IsExpired(s.now())))

			return ErrPasswordResetNotUsable
		}

		// This, not the check above, is what makes the link single-use. Two
		// confirmations arriving together both read an open row; only one of them
		// updates a row here, and the other is told what everybody holding a
		// spent link is told.
		switch stamped, err := store.UsePasswordReset(ctx, reset.ID); {
		case err != nil:
			return fmt.Errorf("use password reset: %w", err)
		case stamped == 0:
			return ErrPasswordResetNotUsable
		}

		// The account comes from the reset row, never from the request. It is the
		// whole of what the link stands for, and letting whoever holds it name an
		// account would make one reset a way into any account.
		user, err := store.SetUserPasswordHash(ctx, identityrepo.SetUserPasswordHashParams{
			ID:           reset.UserID,
			PasswordHash: hash,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The account was masked between the link going out and being
				// followed. Same answer as a spent link: there is nothing behind
				// it either way.
				s.log.InfoContext(ctx, "password reset refused",
					slog.String("password_reset_id", reset.ID),
					slog.String("reason", "the account is no longer live"))

				return ErrPasswordResetNotUsable
			}

			return fmt.Errorf("set password: %w", err)
		}

		revoked, err := store.DeleteAllSessionsForUser(ctx, user.ID)
		if err != nil {
			return fmt.Errorf("revoke sessions: %w", err)
		}

		s.log.InfoContext(ctx, "password reset confirmed",
			slog.String("user_id", user.ID),
			slog.Int64("sessions_revoked", revoked))

		return nil
	})
}

// allowed asks the per-account bucket whether this address may ask again.
//
// It fails **closed**. This is an unauthenticated endpoint that causes mail to
// be sent to somebody else's inbox, and docs/nfr.md keeps fail-closed for that
// shape — the same call ADR-0010 makes for the login path, for the same reason:
// a limiter that is off is indistinguishable from one that is not there.
func (s *PasswordResets) allowed(ctx context.Context, address string) (time.Duration, error) {
	ok, wait, err := s.perAccount.Allow(ctx, accountKey(address))
	if err != nil {
		// The key is not logged: it is a hashed address, and this line is
		// written on every request while Redis is down.
		s.log.ErrorContext(ctx, "password reset rate limit unavailable; failing closed",
			slog.String("error", err.Error()))

		return retryAfterUnavailable, ErrTooManyAttempts
	}

	if !ok {
		return wait, ErrTooManyAttempts
	}

	return 0, nil
}

// message is the reset as it arrives. Written here rather than in a template,
// for the reason given on Invitations.message.
//
// It says what to do if the reader did not ask for it, and that line is not
// decoration: a reset mail arriving unasked is the earliest signal somebody gets
// that an address of theirs is being probed.
func (s *PasswordResets) message(address, token string) mail.Message {
	link := s.baseURL.JoinPath("reset-password", token).String()

	return mail.Message{
		To:      address,
		Subject: "Atur ulang sandi Project Management",
		Text: strings.Join([]string{
			"Halo,",
			"",
			"Ada yang meminta pengaturan ulang sandi untuk akun ini. Buka tautan di bawah untuk memilih sandi baru:",
			"",
			link,
			"",
			fmt.Sprintf("Tautan ini berlaku %d menit dan hanya bisa dipakai sekali.", int(identitydom.PasswordResetWindow.Minutes())),
			"Setelah sandi diganti, semua perangkat yang sedang masuk akan dikeluarkan.",
			"",
			"Kalau Anda tidak meminta ini, abaikan saja pesannya — sandi Anda tidak berubah.",
		}, "\n"),
	}
}
