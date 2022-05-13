package repositories

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/georgysavva/scany/pgxscan"
	"github.com/jackc/pgx/v4"
)

const (
	SURFBOARD_PATH = "root.1"
	SNOWBOARD_PATH = "root.2"
	PER_PAGE_MAX   = 25
)

type Post struct {
	Id              string     `json:"id" db:"id"`
	UserId          string     `json:"userId" db:"user_id"`
	Title           string     `json:"title" db:"title"`
	Price           float32    `json:"price" db:"price"`
	Rate            string     `json:"rate" db:"rate"`
	Description     *string    `json:"description" db:"description"`
	PickupLatitude  *float64   `json:"pickupLatitude" db:"pickup_latitude"`
	PickupLongitude *float64   `json:"pickupLongitude" db:"pickup_longitude"`
	CreatedAt       *time.Time `json:"createdAt" db:"created_at"`
	DeletedAt       *time.Time `db:"deleted_at"`

	Email      *string     `json:"email" db:"email"`
	AvatarUrl  *string     `json:"avatarUrl" db:"avatar_url"`
	Categories *string     `json:"categories" db:"categories"`
	Tags       *string     `json:"tags" db:"tags"`
	Medias     []PostMedia `json:"medias" db:"medias"`
}

type PostMedia struct {
	Id        int        `json:"id" db:"id"`
	PostId    string     `json:"postId" db:"post_id"`
	MediaUrl  string     `json:"mediaUrl" db:"media_url"`
	Type      string     `json:"type" db:"type"`
	CreatedAt *time.Time `json:"createdAt" db:"created_at"`
	DeletedAt *time.Time `db:"deleted_at"`
}

type CreatePost struct {
	UserId          string   `json:"userId"`
	Title           string   `json:"title"`
	Price           float32  `json:"price"`
	Rate            string   `json:"rate"`
	Description     *string  `json:"description"`
	PickupLatitude  *float64 `json:"pickupLatitude"`
	PickupLongitude *float64 `json:"pickupLongitude"`
}

type CreatePostTag struct {
	PostId string
	TagId  string
}

type CreatePostMedia struct {
	PostId   string
	MediaUrl string
	Type     string
}

type CreatePostCategory struct {
	PostId     string
	CategoryId string
}

// TODO: Replace params with filters
func (r *Repository) GetPosts(ctx context.Context, params url.Values) ([]Post, error) {
	var rootPath string
	if params.Get("type") == "snowboard" {
		rootPath = SNOWBOARD_PATH
	} else {
		rootPath = SURFBOARD_PATH
	}

	cols := []string{
		"a.id",
		"a.user_id",
		"a.title",
		"a.price",
		"a.rate",
		"a.pickup_latitude",
		"a.pickup_longitude",
		"a.created_at",
		"b.email",
		"b.avatar_url",
		`string_agg(DISTINCT d. "value", ',') AS categories`,
		`string_agg(DISTINCT f. "value", ',') AS tags`,
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	sqlBuilder := psql.Select(cols...).From("post a").
		Join(`"user" b ON a.user_id = b.id`).
		Join("post_category c ON a.id = c.post_id").
		Join("category d ON c.category_id = d.id").
		LeftJoin("post_tag e ON a.id = e.post_id").
		LeftJoin("tag f ON e.tag_id = f.id").
		Where("d.path <@ ?", rootPath)

	if categories := params.Get("cats"); categories != "" {
		sqlBuilder = sqlBuilder.Where(sq.Eq{"d.value": strings.Split(categories, ",")})
	}

	if tags := params.Get("tags"); tags != "" {
		sqlBuilder = sqlBuilder.Where(sq.Eq{"f.value": strings.Split(tags, ",")})
	}

	offset := 0
	if page := params.Get("p"); page != "" {
		var err error
		offset, err = strconv.Atoi(page)
		if err != nil {
			offset = 0
		}
	}

	var sqlStmt string
	var sqlArgs []interface{}
	{
		var err error
		sqlStmt, sqlArgs, err = sqlBuilder.Offset(uint64(offset)*PER_PAGE_MAX).
			Limit(PER_PAGE_MAX).
			GroupBy("a.id", "b.id").
			ToSql()
		if err != nil {
			return nil, fmt.Errorf("failed to build query: %s | %w", sqlStmt, err)
		}
	}

	var posts []Post
	err := pgxscan.Select(ctx, r.db, &posts, sqlStmt, sqlArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute: %s | %w", sqlStmt, err)
	}

	return posts, nil
}

func (r *Repository) GetPost(ctx context.Context, id string) (*Post, error) {
	cols := []string{
		"a.id",
		"a.user_id",
		"a.title",
		"a.description",
		"a.price",
		"a.rate",
		"a.pickup_latitude",
		"a.pickup_longitude",
		"a.created_at",
		"b.email",
		"b.avatar_url",
		`string_agg(DISTINCT d. "value", ',') AS categories`,
		`string_agg(DISTINCT f. "value", ',') AS tags`,
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	sqlStmt, sqlArgs, err := psql.Select(cols...).
		From("post a").
		Join(`"user" b ON a.user_id = b.id`).
		Join("post_category c ON a.id = c.post_id").
		Join("category d ON c.category_id = d.id").
		LeftJoin("post_tag e ON a.id = e.post_id").
		LeftJoin("tag f ON e.tag_id = f.id").
		Where(sq.Eq{"a.id": id}).
		GroupBy("a.id", "b.id").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %s | %w", sqlStmt, err)
	}

	var post Post
	{
		err := pgxscan.Get(ctx, r.db, &post, sqlStmt, sqlArgs...)
		if err != nil {
			return nil, fmt.Errorf("failed to execute: %s | %w", sqlStmt, err)
		}
	}

	if err := r.setPostMedias(ctx, &post); err != nil {
		return nil, fmt.Errorf("failed to set post medias | %w", err)
	}

	return &post, nil
}

func (r *Repository) CreatePost(ctx context.Context, payload CreatePost) (post *Post, err error) {
	tx := ctx.Value(TxnKey).(pgx.Tx)
	if tx == nil {
		tx, _ = r.db.Begin(ctx)
		defer func() error {
			if err != nil {
				return tx.Rollback(ctx)
			}
			return tx.Commit(ctx)
		}()
	}

	cols := []string{
		"user_id",
		"title",
		"price",
		"rate",
		"description",
		"pickup_latitude",
		"pickup_longitude",
	}

	vals := []interface{}{
		payload.UserId,
		payload.Title,
		payload.Price,
		payload.Rate,
		payload.Description,
		payload.PickupLatitude,
		payload.PickupLongitude,
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	sqlStmt, sqlArgs, err := psql.Insert("post").
		Columns(cols...).
		Values(vals...).
		Suffix("RETURNING id").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	var newPost Post
	if err := tx.QueryRow(ctx, sqlStmt, sqlArgs...).Scan(&newPost.Id); err != nil {
		return nil, fmt.Errorf("failed to execute: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	return &newPost, nil
}

func (r *Repository) CreatePostTags(ctx context.Context, tags []CreatePostTag) (err error) {
	tx := ctx.Value(TxnKey).(pgx.Tx)
	if tx == nil {
		tx, _ = r.db.Begin(ctx)
		defer func() error {
			if err != nil {
				return tx.Rollback(ctx)
			}
			return tx.Commit(ctx)
		}()
	}

	cols := []string{
		"post_id",
		"tag_id",
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar).
		Insert("post_tag").
		Columns(cols...)
	for idx := range tags {
		psql = psql.Values(
			tags[idx].PostId,
			tags[idx].TagId,
		)
	}

	sqlStmt, sqlArgs, err := psql.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	if _, err = tx.Exec(ctx, sqlStmt, sqlArgs...); err != nil {
		return fmt.Errorf("failed to execute query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	return nil
}

func (r *Repository) CreatePostMedias(ctx context.Context, medias []CreatePostMedia) (err error) {
	tx := ctx.Value(TxnKey).(pgx.Tx)
	if tx == nil {
		tx, _ = r.db.Begin(ctx)
		defer func() error {
			if err != nil {
				return tx.Rollback(ctx)
			}
			return tx.Commit(ctx)
		}()
	}

	cols := []string{
		"post_id",
		"media_url",
		"type",
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar).
		Insert("post_media").
		Columns(cols...)
	for idx := range medias {
		psql = psql.Values(
			medias[idx].PostId,
			medias[idx].MediaUrl,
			medias[idx].Type,
		)
	}

	sqlStmt, sqlArgs, err := psql.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	if _, err = tx.Exec(ctx, sqlStmt, sqlArgs...); err != nil {
		return fmt.Errorf("failed to execute query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	return nil
}

func (r *Repository) CreatePostCategories(ctx context.Context, categories []CreatePostCategory) (err error) {
	tx := ctx.Value(TxnKey).(pgx.Tx)
	if tx == nil {
		tx, _ = r.db.Begin(ctx)
		defer func() error {
			if err != nil {
				return tx.Rollback(ctx)
			}
			return tx.Commit(ctx)
		}()
	}

	cols := []string{
		"post_id",
		"category_id",
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar).
		Insert("post_category").
		Columns(cols...)
	for idx := range categories {
		psql = psql.Values(
			categories[idx].PostId,
			categories[idx].CategoryId,
		)
	}

	sqlStmt, sqlArgs, err := psql.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	if _, err = tx.Exec(ctx, sqlStmt, sqlArgs...); err != nil {
		return fmt.Errorf("failed to execute query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	return nil
}

func (r *Repository) setPostMedias(ctx context.Context, post *Post) error {
	cols := []string{
		"id",
		"post_id",
		"media_url",
		"type",
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	sqlStmt, sqlArgs, err := psql.Select(cols...).
		From("post_media").
		Where(sq.Eq{"post_id": post.Id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	if err := pgxscan.Select(ctx, r.db, &post.Medias, sqlStmt, sqlArgs...); err != nil {
		return fmt.Errorf("failed to execute query: %s args: %v | %w", sqlStmt, sqlArgs, err)
	}

	return nil
}
