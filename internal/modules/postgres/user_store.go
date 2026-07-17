package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// userStore is the PostgreSQL contracts.UserStore. It owns SQL and row
// mapping and returns only domain.User values across the boundary
// (MEG-015 §05).
type userStore struct {
	q queryer
}

// NewUserStore builds a pool-backed UserStore for the direct (non-
// transactional) read path used during authentication and queries.
func NewUserStore(pool *pgxpool.Pool) contracts.UserStore {
	return &userStore{q: pool}
}

const userColumns = `id, username, email, display_name, status, created_at, updated_at`

func (s *userStore) Create(ctx context.Context, user domain.User) (domain.User, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO users (id, username, email, display_name, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		string(user.ID), user.Username, user.Email, user.DisplayName, string(user.Status), user.CreatedAt, user.UpdatedAt,
	)
	if err != nil {
		return domain.User{}, mapError("create user", err)
	}
	return user, nil
}

func (s *userStore) FindByID(ctx context.Context, id domain.UserID) (domain.User, error) {
	row := s.q.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, string(id))
	user, err := scanUser(row)
	if err != nil {
		if isNoRows(err) {
			return domain.User{}, contracts.NewError(contracts.NotFound, "user not found")
		}
		return domain.User{}, mapError("find user by id", err)
	}
	return user, nil
}

func (s *userStore) FindByUsername(ctx context.Context, username string) (domain.User, error) {
	row := s.q.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE username = $1`, username)
	user, err := scanUser(row)
	if err != nil {
		if isNoRows(err) {
			return domain.User{}, contracts.NewError(contracts.NotFound, "user not found")
		}
		return domain.User{}, mapError("find user by username", err)
	}
	return user, nil
}

func (s *userStore) Update(ctx context.Context, user domain.User) (domain.User, error) {
	tag, err := s.q.Exec(ctx,
		`UPDATE users SET username = $2, email = $3, display_name = $4, status = $5, updated_at = $6 WHERE id = $1`,
		string(user.ID), user.Username, user.Email, user.DisplayName, string(user.Status), user.UpdatedAt,
	)
	if err != nil {
		return domain.User{}, mapError("update user", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.User{}, contracts.NewError(contracts.NotFound, "user not found")
	}
	return user, nil
}

func (s *userStore) List(ctx context.Context) ([]domain.User, error) {
	rows, err := s.q.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at, id`)
	if err != nil {
		return nil, mapError("list users", err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, mapError("scan user row", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list users", err)
	}
	return users, nil
}

func scanUser(row pgx.Row) (domain.User, error) {
	var (
		user   domain.User
		id     string
		status string
	)
	if err := row.Scan(&id, &user.Username, &user.Email, &user.DisplayName, &status, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return domain.User{}, err
	}
	user.ID = domain.UserID(id)
	user.Status = domain.UserStatus(status)
	return user, nil
}
