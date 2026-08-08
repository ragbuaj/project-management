package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ragbuaj/project-management/backend/internal/authz"
	"github.com/ragbuaj/project-management/backend/internal/mail"
	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
)

// InvitationStore is the part of the repository this service uses, declared at
// the consumer for the same reason SessionStore is.
//
// Sending an invitation and redeeming one share this interface, and therefore
// share one InTx, because they are two halves of the same transaction boundary.
// A second store with a second InTx beside it would let the two halves drift
// into two different answers about what a transaction is.
type InvitationStore interface {
	CreateInvitation(ctx context.Context, arg identityrepo.CreateInvitationParams) (identityrepo.CreateInvitationRow, error)
	ExpireOpenInvitationsForEmail(ctx context.Context, email string) (int64, error)
	GetUserByEmail(ctx context.Context, email string) (identityrepo.GetUserByEmailRow, error)

	GetInvitationByTokenHash(ctx context.Context, tokenHash []byte) (identityrepo.GetInvitationByTokenHashRow, error)
	AcceptInvitation(ctx context.Context, id string) (int64, error)
	CreateUser(ctx context.Context, arg identityrepo.CreateUserParams) (identityrepo.CreateUserRow, error)
}

// Sender delivers one message. It is an interface here, and a mail.SMTP or a
// mail.Capture on the other side, so that the tests of this service can read
// what was sent without an SMTP server.
type Sender interface {
	Send(ctx context.Context, m mail.Message) error
}

// InTx runs fn against a store bound to one transaction, committing if it
// returns nil and rolling back otherwise.
//
// The transaction boundary belongs to this layer (ADR-0008, rules/20-go.md),
// but pgx does not: taking a function keeps the service testable with an
// ordinary fake instead of something that has to impersonate pgx.Tx.
type InTx func(ctx context.Context, fn func(InvitationStore) error) error

var (
	// ErrInvalidEmail means the address is not one a message could be sent to.
	ErrInvalidEmail = errors.New("invalid email address")

	// ErrRoleNotInvitable covers both an unknown role and `owner`. There is
	// exactly one owner and that account is the first one, not an invited one —
	// the CHECK on invitations.role refuses it too, and this is the same refusal
	// arriving as a 422 rather than as a constraint violation.
	ErrRoleNotInvitable = errors.New("role cannot be invited")

	// ErrEmailTaken means an account already exists for the address. Invitations
	// create accounts and nothing else; somebody who already has one is added to
	// a folder or project directly, with no invitation involved.
	//
	// Redemption returns it too, from the unique index rather than from a
	// lookup: between sending a link and following it, the address may have got
	// an account by some other route.
	ErrEmailTaken = errors.New("an account already exists for this address")

	// ErrInvitationNotUsable is the one answer to a link that was never issued,
	// one that has expired, one that has already created an account, and one
	// that is not shaped like a token at all.
	//
	// They are four different facts and the caller must not be able to tell them
	// apart: answering "expired" rather than "unknown" confirms that the address
	// holding the link was invited, which is the enumeration docs/threat-model.md
	// closes on this path. The difference belongs in the log, not in the
	// response.
	ErrInvitationNotUsable = errors.New("this invitation link cannot be used")

	// ErrInvalidName means the name the invitee chose is not one that can be
	// stored. The column carries a CHECK that it is not blank, and this is that
	// refusal arriving as a 422 rather than as a constraint violation.
	ErrInvalidName = errors.New("invalid name")

	// ErrUndeliverable means the invitation was recorded but nobody could be
	// told about it. docs/nfr.md is explicit that this is shown as a failure
	// rather than swallowed: an invitation nobody receives is not an invitation.
	ErrUndeliverable = errors.New("the invitation could not be delivered")
)

// maxEmailLength bounds what reaches the database and the mail server. The
// column is text, and RFC 5321 caps a path at 256 octets, so anything past this
// is not an address somebody typed.
const maxEmailLength = 254

// maxNameLength is the 200 characters docs/api/openapi.yaml already declares
// for every other name in this application. Characters rather than bytes,
// because that is what a maxLength in the contract counts and what somebody
// writing their own name counts.
const maxNameLength = 200

// uniqueViolation is the SQLSTATE PostgreSQL raises for a unique index. It is
// written out rather than pulled from github.com/jackc/pgerrcode: one constant
// is not worth a dependency, and this code has not changed since PostgreSQL 7.
const uniqueViolation = "23505"

// Invitations creates the accounts of employees who do not have one yet.
type Invitations struct {
	inTx   InTx
	sender Sender
	// baseURL is where the link in the message points. It comes from
	// APP_BASE_URL rather than from the request, because a Host header is
	// attacker-controlled and this link is the one that sets a password.
	baseURL *url.URL
	log     *slog.Logger
	now     func() time.Time
}

// NewInvitations wires the service.
func NewInvitations(inTx InTx, sender Sender, baseURL *url.URL, log *slog.Logger, now func() time.Time) *Invitations {
	return &Invitations{inTx: inTx, sender: sender, baseURL: baseURL, log: log, now: now}
}

// Invited is what the caller learns about an invitation it just created. The
// token is not among it: the link exists in the message and nowhere else, so a
// re-send has to mint a new one.
type Invited struct {
	ID        string
	Email     string
	Role      string
	ExpiresAt time.Time
}

// Create records an invitation and sends the link.
//
// The message is sent after the transaction commits, not inside it. Holding a
// database transaction open across an SMTP round trip would mean holding it for
// as long as the mail server feels like taking — up to the whole send timeout —
// and that is a lock held on the invitations table for a reason that has
// nothing to do with the database. The cost of the other order is stated on the
// delivery failure below.
func (s *Invitations) Create(ctx context.Context, invitedBy, email, role string) (Invited, error) {
	address, err := normalizeEmail(email)
	if err != nil {
		return Invited{}, err
	}

	if err := checkInvitable(role); err != nil {
		return Invited{}, err
	}

	token, digest, err := identitydom.NewInvitationToken()
	if err != nil {
		return Invited{}, fmt.Errorf("new invitation token: %w", err)
	}

	var created identityrepo.CreateInvitationRow

	err = s.inTx(ctx, func(store InvitationStore) error {
		switch _, err := store.GetUserByEmail(ctx, address); {
		case err == nil:
			return ErrEmailTaken
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("look up account: %w", err)
		}

		// Any link already sent to this address stops working here, so that one
		// address never has two live invitations.
		if _, err := store.ExpireOpenInvitationsForEmail(ctx, address); err != nil {
			return fmt.Errorf("supersede open invitations: %w", err)
		}

		created, err = store.CreateInvitation(ctx, identityrepo.CreateInvitationParams{
			Email:     address,
			TokenHash: digest,
			InvitedBy: invitedBy,
			Role:      role,
			ExpiresAt: pgtype.Timestamptz{Time: identitydom.NewInvitationExpiry(s.now()), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("create invitation: %w", err)
		}

		return nil
	})
	if err != nil {
		return Invited{}, err
	}

	invited := Invited{
		ID:        created.ID,
		Email:     created.Email,
		Role:      created.Role,
		ExpiresAt: created.ExpiresAt.Time,
	}

	if err := s.sender.Send(ctx, s.message(address, token)); err != nil {
		// The row stays. It carries no address anyone can reach it by — the
		// token was never stored — so it is inert until it expires, and the
		// owner's retry supersedes it. Rolling back instead would need the send
		// inside the transaction, which is the lock held across SMTP above.
		s.log.ErrorContext(ctx, "invitation recorded but not delivered",
			slog.String("invitation_id", created.ID),
			slog.String("error", err.Error()))

		return invited, fmt.Errorf("%w: %w", ErrUndeliverable, err)
	}

	return invited, nil
}

// Accept turns a link into an account.
//
// The stamp on the invitation and the INSERT of the account are one
// transaction, so a redemption that loses the race creates nothing: the stamp
// updates no rows, this returns before CreateUser, and nothing is committed.
// Two people clicking the same link at the same moment therefore end with one
// account rather than two, or with one account and one orphaned invitation.
//
// The password is hashed before the transaction opens, not inside it. Argon2id
// is expensive on purpose — 19 MiB and two passes, ADR-0005 — and a transaction
// held open across it is a row lock on invitations held for the length of a
// deliberately slow computation. It is the same choice as sending the message
// after the commit rather than inside it, for the same reason.
func (s *Invitations) Accept(ctx context.Context, token, name, password string) (User, error) {
	digest, err := identitydom.InvitationTokenDigest(token)
	if err != nil {
		// A link that is not shaped like one is answered exactly as an unknown
		// link, and the shape is the only thing that has been learned.
		s.log.InfoContext(ctx, "invitation redemption refused",
			slog.String("reason", "the token is malformed"))

		return User{}, ErrInvitationNotUsable
	}

	fullName, err := normalizeName(name)
	if err != nil {
		return User{}, err
	}

	// Whatever ADR-0009 rules on the password reaches the caller as it is:
	// unlike the invitation, the password is something they can still fix, and
	// telling them nothing is what makes an account-setup form unusable. The
	// breach blocklist joins this call in piece h.
	hash, err := identitydom.HashPassword(password)
	if err != nil {
		return User{}, err
	}

	var created identityrepo.CreateUserRow

	err = s.inTx(ctx, func(store InvitationStore) error {
		row, err := store.GetInvitationByTokenHash(ctx, digest)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				s.log.InfoContext(ctx, "invitation redemption refused",
					slog.String("reason", "no invitation carries this token"))

				return ErrInvitationNotUsable
			}

			return fmt.Errorf("get invitation: %w", err)
		}

		invitation := identitydom.Invitation{
			ID:         row.ID,
			Email:      row.Email,
			Role:       row.Role,
			InvitedBy:  row.InvitedBy,
			CreatedAt:  row.CreatedAt.Time,
			ExpiresAt:  row.ExpiresAt.Time,
			AcceptedAt: timestamp(row.AcceptedAt),
		}

		if !invitation.IsUsable(s.now()) {
			// Here is where the two facts the client is not told apart are kept
			// apart, so that somebody debugging a link an employee cannot use
			// can find out which of the two it was.
			s.log.InfoContext(ctx, "invitation redemption refused",
				slog.String("invitation_id", invitation.ID),
				slog.Bool("already_accepted", invitation.IsAccepted()),
				slog.Bool("expired", invitation.IsExpired(s.now())))

			return ErrInvitationNotUsable
		}

		// This, not the check above, is what makes the link single-use. Two
		// clicks arriving together both read an open invitation; only one of
		// them updates a row here, and the other one is told what everybody
		// holding a spent link is told.
		switch stamped, err := store.AcceptInvitation(ctx, invitation.ID); {
		case err != nil:
			return fmt.Errorf("accept invitation: %w", err)
		case stamped == 0:
			return ErrInvitationNotUsable
		}

		// The address comes from the invitation, never from the request. It is
		// the whole of what the owner approved, and letting the person holding
		// the link supply their own would make an invitation to one address a
		// way to create an account on any other.
		created, err = store.CreateUser(ctx, identityrepo.CreateUserParams{
			Email:        invitation.Email,
			Name:         fullName,
			PasswordHash: hash,
			Role:         invitation.Role,
		})
		if err != nil {
			if isUniqueViolation(err) {
				return ErrEmailTaken
			}

			return fmt.Errorf("create user: %w", err)
		}

		return nil
	})
	if err != nil {
		return User{}, err
	}

	return User{
		ID:       created.ID,
		Email:    created.Email,
		Name:     created.Name,
		Timezone: created.Timezone,
		Role:     created.Role,
	}, nil
}

// message is the invitation as it arrives. It is written here rather than in a
// template: it is four lines, and a template engine would put them one
// indirection away from the code that decides what they say.
func (s *Invitations) message(address, token string) mail.Message {
	link := s.baseURL.JoinPath("invite", token).String()

	return mail.Message{
		To:      address,
		Subject: "Anda diundang ke Project Management",
		Text: strings.Join([]string{
			"Halo,",
			"",
			"Akun Anda sudah disiapkan. Buka tautan di bawah untuk memilih sandi:",
			"",
			link,
			"",
			fmt.Sprintf("Tautan ini berlaku %d hari dan hanya bisa dipakai sekali.", int(identitydom.InvitationWindow.Hours()/24)),
			"Kalau Anda tidak mengharapkan undangan ini, abaikan saja pesannya.",
		}, "\n"),
	}
}

// normalizeEmail checks the address and returns the form that is stored.
//
// The address is kept as it was typed apart from surrounding space: the schema
// indexes lower(email) and every lookup goes through lower(), so lowercasing
// here would only throw away how somebody writes their own name.
func normalizeEmail(email string) (string, error) {
	address := strings.TrimSpace(email)

	if address == "" {
		return "", fmt.Errorf("%w: it is empty", ErrInvalidEmail)
	}

	if len(address) > maxEmailLength {
		return "", fmt.Errorf("%w: it is longer than %d bytes", ErrInvalidEmail, maxEmailLength)
	}

	parsed, err := netmail.ParseAddress(address)
	if err != nil {
		return "", fmt.Errorf("%w: it is not an address", ErrInvalidEmail)
	}

	// "Budi <budi@example.com>" parses, and inviting it would store a display
	// name in a column every lookup compares against a bare address.
	if parsed.Address != address {
		return "", fmt.Errorf("%w: it must be a bare address", ErrInvalidEmail)
	}

	return address, nil
}

// normalizeName checks the name the invitee chose and returns what is stored.
//
// Only the surrounding space is taken off. What somebody writes their own name
// as is theirs, and an application that title-cases it or strips the characters
// it did not expect is an application that gets people's names wrong.
func normalizeName(name string) (string, error) {
	full := strings.TrimSpace(name)

	if full == "" {
		return "", fmt.Errorf("%w: it is empty", ErrInvalidName)
	}

	if utf8.RuneCountInString(full) > maxNameLength {
		return "", fmt.Errorf("%w: it is longer than %d characters", ErrInvalidName, maxNameLength)
	}

	return full, nil
}

// timestamp turns a nullable column into the nil-or-value the domain layer
// reads. The domain imports nothing from the repository (ADR-0008), so pgtype
// stops here.
func timestamp(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}

	at := v.Time

	return &at
}

// isUniqueViolation reports whether err is PostgreSQL refusing a duplicate.
//
// The unique index is what actually decides that an address has one account,
// rather than a SELECT before the INSERT: two redemptions of two invitations to
// the same address, committing at the same moment, both see no account and only
// the index stops the second one.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

// checkInvitable refuses roles that must not be handed out by invitation.
//
// The vocabulary comes from authz rather than from a second list here, so a
// role added there cannot be quietly missing from this check — and the one role
// singled out is singled out by name.
func checkInvitable(role string) error {
	parsed, err := authz.ParseRole(role)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRoleNotInvitable, err)
	}

	if parsed == authz.RoleOwner {
		return fmt.Errorf("%w: there is one owner, and it is not an invited account", ErrRoleNotInvitable)
	}

	return nil
}
