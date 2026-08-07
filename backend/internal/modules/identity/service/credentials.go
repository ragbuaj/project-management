package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
)

// UserStore is the part of the repository the credential check uses.
type UserStore interface {
	GetUserByEmail(ctx context.Context, email string) (identityrepo.GetUserByEmailRow, error)
	UpdateUserPasswordHash(ctx context.Context, arg identityrepo.UpdateUserPasswordHashParams) (int64, error)
}

// ErrInvalidCredentials is the single answer to an address with no account and
// to a wrong password. ADR-0005 requires the two to be indistinguishable, and
// ADR-0010 extends that to the time each takes.
var ErrInvalidCredentials = errors.New("email or password is wrong")

// User is the caller as the rest of the application sees them. It carries no
// hash: the only code that needs one is in this file.
type User struct {
	ID       string
	Email    string
	Name     string
	Timezone string
	// Role is the account role from ADR-0012, as stored. It travels as a
	// string because this module does not decide permissions — internal/authz
	// does, and it owns the parsing.
	Role string
}

// Credentials checks passwords.
type Credentials struct {
	store UserStore
	log   *slog.Logger

	// decoy is a real hash of a password nobody knows. It is verified against
	// when no account matches, so that a request for an unknown address costs
	// the same 19 MiB and the same two passes as a request for a known one.
	//
	// Without it the login endpoint answers "no such account" faster than
	// "wrong password", and a stopwatch turns it into an address checker.
	decoy string
}

// NewCredentials wires the service and builds the decoy hash.
//
// The decoy is computed once here rather than written into the source as a
// constant: a hash in the repository is a hash an attacker can rule out, and
// then the timing gap is back for anyone who noticed. It costs one Argon2id
// run at start-up.
func NewCredentials(store UserStore, log *slog.Logger) (*Credentials, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("read decoy secret: %w", err)
	}

	decoy, err := identitydom.HashPassword(base64.RawURLEncoding.EncodeToString(secret))
	if err != nil {
		return nil, fmt.Errorf("hash decoy: %w", err)
	}

	return &Credentials{store: store, log: log, decoy: decoy}, nil
}

// Authenticate returns the user behind an email and password.
//
// Every failure returns ErrInvalidCredentials, and every failure costs the
// same work. A caller who can tell "no account" from "wrong password" — by the
// answer or by the clock — has an endpoint that confirms which addresses have
// accounts.
func (c *Credentials) Authenticate(ctx context.Context, email, password string) (User, error) {
	row, err := c.store.GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return User{}, fmt.Errorf("get user by email: %w", err)
		}

		// Verified and discarded. The comparison cannot succeed; running it is
		// the entire point.
		_ = identitydom.VerifyPassword(c.decoy, password)

		return User{}, ErrInvalidCredentials
	}

	if err := identitydom.VerifyPassword(row.PasswordHash, password); err != nil {
		if errors.Is(err, identitydom.ErrHashUnreadable) {
			// Not a wrong password: a row this application could not have
			// written. The caller still gets the same refusal, but somebody
			// has to be able to find out why nobody can sign in.
			c.log.ErrorContext(ctx, "stored password hash is unreadable",
				slog.String("user_id", row.ID),
				slog.String("error", err.Error()))
		}

		return User{}, ErrInvalidCredentials
	}

	c.rehash(ctx, row.ID, row.PasswordHash, password)

	return User{
		ID:       row.ID,
		Email:    row.Email,
		Name:     row.Name,
		Timezone: row.Timezone,
		Role:     row.Role,
	}, nil
}

// rehash rewrites a hash that was made at a cost this application has since
// raised. A successful login is the only moment it can be done, because it is
// the only moment the password is in memory.
//
// A failure here is logged and the login continues. The account is still
// reachable at its old cost, which is where it already was; refusing the login
// would take something away instead.
func (c *Credentials) rehash(ctx context.Context, userID, currentHash, password string) {
	if !identitydom.NeedsRehash(currentHash) {
		return
	}

	updated, err := identitydom.HashPassword(password)
	if err != nil {
		c.log.WarnContext(ctx, "password was not rehashed",
			slog.String("user_id", userID),
			slog.String("error", err.Error()))

		return
	}

	if _, err := c.store.UpdateUserPasswordHash(ctx, identityrepo.UpdateUserPasswordHashParams{
		ID:          userID,
		NewHash:     updated,
		CurrentHash: currentHash,
	}); err != nil {
		c.log.WarnContext(ctx, "password was not rehashed",
			slog.String("user_id", userID),
			slog.String("error", err.Error()))
	}
}
