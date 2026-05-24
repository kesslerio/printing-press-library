package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mvanhorn/printing-press-library/library/commerce/instacart/internal/config"
)

// StoreSchemaVersion is the on-disk schema version this binary understands.
const StoreSchemaVersion = 3

type Store struct {
	db   *sql.DB
	path string
}

func Open(learnPath ...string) (*Store, error) {
	if len(learnPath) > 0 && learnPath[0] != "" {
		return OpenAt(learnPath[0])
	}
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "instacart.db")
	return OpenAt(path)
}

// OpenAt opens a Store at a specific path. Exposed for tests that need
// isolation from the user's default ~/.config/instacart/instacart.db;
// normal callers should use Open().
func OpenAt(path string) (*Store, error) {
	return openAt(path)
}

func openAt(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS persisted_ops (
			operation_name TEXT PRIMARY KEY,
			sha256_hash TEXT NOT NULL,
			captured_at INTEGER NOT NULL,
			sample_variables TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS retailers (
			slug TEXT PRIMARY KEY,
			retailer_id TEXT,
			shop_id TEXT,
			zone_id TEXT,
			name TEXT,
			location_id TEXT,
			updated_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS products (
			item_id TEXT PRIMARY KEY,
			product_id TEXT,
			retailer_slug TEXT,
			name TEXT,
			brand TEXT,
			size TEXT,
			price_cents INTEGER,
			currency TEXT,
			in_stock INTEGER,
			updated_at INTEGER
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS products_fts USING fts5(
			item_id UNINDEXED,
			retailer_slug UNINDEXED,
			name,
			brand,
			size,
			tokenize = 'porter unicode61'
		)`,
		`CREATE TABLE IF NOT EXISTS carts (
			cart_id TEXT PRIMARY KEY,
			retailer_slug TEXT,
			shop_id TEXT,
			item_count INTEGER,
			subtotal_cents INTEGER,
			currency TEXT,
			updated_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS cart_items (
			cart_id TEXT NOT NULL,
			item_id TEXT NOT NULL,
			quantity REAL,
			quantity_type TEXT,
			name TEXT,
			price_cents INTEGER,
			PRIMARY KEY (cart_id, item_id)
		)`,
		`CREATE TABLE IF NOT EXISTS inventory_tokens (
			retailer_slug TEXT PRIMARY KEY,
			token TEXT NOT NULL,
			location_id TEXT,
			shop_id TEXT,
			zone_id TEXT,
			fetched_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		// orders: one row per past Instacart order for the authenticated user.
		// Populated by `history sync` from the CustomerOrderHistory GraphQL op.
		`CREATE TABLE IF NOT EXISTS orders (
			order_id TEXT PRIMARY KEY,
			retailer_slug TEXT,
			placed_at INTEGER,
			status TEXT,
			total_cents INTEGER,
			item_count INTEGER,
			synced_at INTEGER NOT NULL
		)`,
		// order_items: one row per item within an order.
		`CREATE TABLE IF NOT EXISTS order_items (
			order_id TEXT NOT NULL,
			item_id TEXT NOT NULL,
			product_id TEXT,
			name TEXT,
			quantity REAL,
			quantity_type TEXT,
			price_cents INTEGER,
			PRIMARY KEY (order_id, item_id)
		)`,
		// purchased_items: aggregated across orders. One row per (retailer, item_id)
		// combination. Tracks purchase_count and last_purchased_at so the
		// history-first resolver can weight recency + frequency.
		`CREATE TABLE IF NOT EXISTS purchased_items (
			item_id TEXT NOT NULL,
			retailer_slug TEXT NOT NULL,
			product_id TEXT,
			name TEXT,
			brand TEXT,
			size TEXT,
			category TEXT,
			last_purchased_at INTEGER,
			first_purchased_at INTEGER,
			purchase_count INTEGER DEFAULT 0,
			last_price_cents INTEGER,
			last_in_stock INTEGER DEFAULT 1,
			PRIMARY KEY (item_id, retailer_slug)
		)`,
		// purchased_items_fts: FTS5 index over purchased_items.name+brand+size+category
		// for the add-time history-first lookup. Rebuilt by sync; updated by the
		// add command on every successful purchase.
		`CREATE VIRTUAL TABLE IF NOT EXISTS purchased_items_fts USING fts5(
			item_id UNINDEXED,
			retailer_slug UNINDEXED,
			name,
			brand,
			size,
			category,
			tokenize = 'porter unicode61'
		)`,
		// search_history: every search query the user has run, keyed by
		// (query, retailer_slug). Helps future retention/analytics and
		// seeds query-completion ideas without re-hitting Instacart.
		`CREATE TABLE IF NOT EXISTS search_history (
			query TEXT NOT NULL,
			retailer_slug TEXT NOT NULL,
			result_count INTEGER,
			first_searched_at INTEGER,
			last_searched_at INTEGER,
			times_searched INTEGER DEFAULT 1,
			PRIMARY KEY (query, retailer_slug)
		)`,
		// history_sync_meta: per-retailer sync state. Per-retailer because a
		// user may have orders across multiple retailers and we sync each
		// independently.
		`CREATE TABLE IF NOT EXISTS history_sync_meta (
			retailer_slug TEXT PRIMARY KEY,
			last_sync_at INTEGER,
			last_sync_status TEXT,
			last_sync_error TEXT,
			last_sync_order_count INTEGER DEFAULT 0,
			last_sync_item_count INTEGER DEFAULT 0,
			opted_out INTEGER DEFAULT 0
		)`,
		// CLI Printing Press: learn migrations
		`CREATE TABLE IF NOT EXISTS search_learnings (
			query_pattern TEXT NOT NULL,
			query_entities TEXT NOT NULL DEFAULT '[]',
			resource_ids TEXT NOT NULL DEFAULT '[]',
			resource_type TEXT NOT NULL,
			venue TEXT,
			action TEXT,
			confidence INTEGER NOT NULL DEFAULT 0,
			source TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (query_pattern, resource_type)
		)`,
		`CREATE TABLE IF NOT EXISTS search_patterns (
			template TEXT NOT NULL,
			entity_kind TEXT NOT NULL,
			confidence INTEGER NOT NULL DEFAULT 0,
			source TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (template, entity_kind)
		)`,
		`CREATE TABLE IF NOT EXISTS entity_lookups (
			canonical TEXT NOT NULL,
			alias TEXT NOT NULL,
			kind TEXT NOT NULL,
			source TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (canonical, alias, kind)
		)`,
		`CREATE TABLE IF NOT EXISTS teach_log_metadata (
			rotation_at DATETIME,
			last_size_bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS search_learnings_fts USING fts5(
			query_pattern, tokenize='porter unicode61'
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate %q: %w", shortStmt(stmt), err)
		}
	}
	return nil
}

func shortStmt(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

type Op struct {
	OperationName string
	Sha256Hash    string
	SampleVars    string
	CapturedAt    time.Time
}

func (s *Store) UpsertOp(op Op) error {
	_, err := s.db.Exec(
		`INSERT INTO persisted_ops(operation_name, sha256_hash, captured_at, sample_variables)
		 VALUES(?, ?, ?, ?)
		 ON CONFLICT(operation_name) DO UPDATE SET
			sha256_hash=excluded.sha256_hash,
			captured_at=excluded.captured_at,
			sample_variables=COALESCE(excluded.sample_variables, sample_variables)`,
		op.OperationName, op.Sha256Hash, time.Now().Unix(), op.SampleVars,
	)
	return err
}

func (s *Store) LookupOp(name string) (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT sha256_hash FROM persisted_ops WHERE operation_name = ?`, name).Scan(&hash)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func (s *Store) CountOps() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM persisted_ops`).Scan(&n)
	return n, err
}

func (s *Store) ListOps() ([]Op, error) {
	rows, err := s.db.Query(`SELECT operation_name, sha256_hash, captured_at FROM persisted_ops ORDER BY operation_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Op
	for rows.Next() {
		var o Op
		var ts int64
		if err := rows.Scan(&o.OperationName, &o.Sha256Hash, &ts); err != nil {
			return nil, err
		}
		o.CapturedAt = time.Unix(ts, 0)
		out = append(out, o)
	}
	return out, rows.Err()
}

type Retailer struct {
	Slug       string
	RetailerID string
	ShopID     string
	ZoneID     string
	Name       string
	LocationID string
}

func (s *Store) UpsertRetailer(r Retailer) error {
	_, err := s.db.Exec(
		`INSERT INTO retailers(slug, retailer_id, shop_id, zone_id, name, location_id, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(slug) DO UPDATE SET
			retailer_id=excluded.retailer_id,
			shop_id=excluded.shop_id,
			zone_id=excluded.zone_id,
			name=excluded.name,
			location_id=excluded.location_id,
			updated_at=excluded.updated_at`,
		r.Slug, r.RetailerID, r.ShopID, r.ZoneID, r.Name, r.LocationID, time.Now().Unix(),
	)
	return err
}

func (s *Store) GetRetailer(slug string) (*Retailer, error) {
	var r Retailer
	err := s.db.QueryRow(
		`SELECT slug, retailer_id, shop_id, zone_id, name, location_id FROM retailers WHERE slug = ?`,
		slug,
	).Scan(&r.Slug, &r.RetailerID, &r.ShopID, &r.ZoneID, &r.Name, &r.LocationID)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListRetailers() ([]Retailer, error) {
	rows, err := s.db.Query(`SELECT slug, retailer_id, shop_id, zone_id, name, location_id FROM retailers ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Retailer
	for rows.Next() {
		var r Retailer
		if err := rows.Scan(&r.Slug, &r.RetailerID, &r.ShopID, &r.ZoneID, &r.Name, &r.LocationID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type InventoryToken struct {
	RetailerSlug string
	Token        string
	LocationID   string
	ShopID       string
	ZoneID       string
	FetchedAt    time.Time
	ExpiresAt    time.Time
}

// UpsertInventoryToken saves an inventory session token for a retailer with a TTL.
func (s *Store) UpsertInventoryToken(t InventoryToken) error {
	_, err := s.db.Exec(
		`INSERT INTO inventory_tokens(retailer_slug, token, location_id, shop_id, zone_id, fetched_at, expires_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(retailer_slug) DO UPDATE SET
			token=excluded.token,
			location_id=excluded.location_id,
			shop_id=excluded.shop_id,
			zone_id=excluded.zone_id,
			fetched_at=excluded.fetched_at,
			expires_at=excluded.expires_at`,
		t.RetailerSlug, t.Token, t.LocationID, t.ShopID, t.ZoneID,
		t.FetchedAt.Unix(), t.ExpiresAt.Unix(),
	)
	return err
}

// GetInventoryToken returns a cached token if present and unexpired.
// Returns (nil, nil) when no cached token exists or the stored one has expired.
func (s *Store) GetInventoryToken(slug string) (*InventoryToken, error) {
	var t InventoryToken
	var fetchedAt, expiresAt int64
	err := s.db.QueryRow(
		`SELECT retailer_slug, token, location_id, shop_id, zone_id, fetched_at, expires_at
		 FROM inventory_tokens WHERE retailer_slug = ?`,
		slug,
	).Scan(&t.RetailerSlug, &t.Token, &t.LocationID, &t.ShopID, &t.ZoneID, &fetchedAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.FetchedAt = time.Unix(fetchedAt, 0)
	t.ExpiresAt = time.Unix(expiresAt, 0)
	if time.Now().After(t.ExpiresAt) {
		return nil, nil
	}
	return &t, nil
}

// ClearInventoryToken invalidates the cached token for a retailer, forcing
// the next search to re-bootstrap via ShopCollectionScoped.
func (s *Store) ClearInventoryToken(slug string) error {
	_, err := s.db.Exec(`DELETE FROM inventory_tokens WHERE retailer_slug = ?`, slug)
	return err
}

type Product struct {
	ItemID       string
	ProductID    string
	RetailerSlug string
	Name         string
	Brand        string
	Size         string
	PriceCents   int64
	Currency     string
	InStock      bool
}

// UpsertProduct stores or updates a resolved product in both products and products_fts.
func (s *Store) UpsertProduct(p Product) error {
	inStock := 0
	if p.InStock {
		inStock = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO products(item_id, product_id, retailer_slug, name, brand, size, price_cents, currency, in_stock, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(item_id) DO UPDATE SET
			product_id=excluded.product_id,
			retailer_slug=excluded.retailer_slug,
			name=excluded.name,
			brand=excluded.brand,
			size=excluded.size,
			price_cents=excluded.price_cents,
			currency=excluded.currency,
			in_stock=excluded.in_stock,
			updated_at=excluded.updated_at`,
		p.ItemID, p.ProductID, p.RetailerSlug, p.Name, p.Brand, p.Size,
		p.PriceCents, p.Currency, inStock, time.Now().Unix(),
	)
	if err != nil {
		return err
	}

	// FTS table: delete + insert (FTS5 upsert dance).
	_, err = tx.Exec(`DELETE FROM products_fts WHERE item_id = ?`, p.ItemID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO products_fts(item_id, retailer_slug, name, brand, size) VALUES(?, ?, ?, ?, ?)`,
		p.ItemID, p.RetailerSlug, p.Name, p.Brand, p.Size,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetProduct returns a cached product by item_id, or nil if not found.
func (s *Store) GetProduct(itemID string) (*Product, error) {
	var p Product
	var inStock int
	err := s.db.QueryRow(
		`SELECT item_id, product_id, retailer_slug, name, brand, size, price_cents, currency, in_stock
		 FROM products WHERE item_id = ?`,
		itemID,
	).Scan(&p.ItemID, &p.ProductID, &p.RetailerSlug, &p.Name, &p.Brand, &p.Size,
		&p.PriceCents, &p.Currency, &inStock)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.InStock = inStock == 1
	return &p, nil
}

// -----------------------------------------------------------------------------
// Purchase history
// -----------------------------------------------------------------------------

// Order is one historical Instacart order for the authenticated user.
type Order struct {
	OrderID      string
	RetailerSlug string
	PlacedAt     time.Time
	Status       string
	TotalCents   int64
	ItemCount    int
}

// OrderItem is one item within a historical order.
type OrderItem struct {
	OrderID      string
	ItemID       string
	ProductID    string
	Name         string
	Quantity     float64
	QuantityType string
	PriceCents   int64
}

// PurchasedItem is an aggregated record across all orders for a
// (retailer_slug, item_id) pair. This is what the history-first resolver
// searches against.
type PurchasedItem struct {
	ItemID           string
	RetailerSlug     string
	ProductID        string
	Name             string
	Brand            string
	Size             string
	Category         string
	LastPurchasedAt  time.Time
	FirstPurchasedAt time.Time
	PurchaseCount    int
	LastPriceCents   int64
	LastInStock      bool
}

// UpsertOrder stores an order header; repeated calls are idempotent
// (ON CONFLICT order_id updates status / totals but not placed_at).
func (s *Store) UpsertOrder(o Order) error {
	_, err := s.db.Exec(
		`INSERT INTO orders(order_id, retailer_slug, placed_at, status, total_cents, item_count, synced_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(order_id) DO UPDATE SET
			status=excluded.status,
			total_cents=excluded.total_cents,
			item_count=excluded.item_count,
			synced_at=excluded.synced_at`,
		o.OrderID, o.RetailerSlug, o.PlacedAt.Unix(), o.Status, o.TotalCents, o.ItemCount, time.Now().Unix(),
	)
	return err
}

// UpsertOrderItem stores one item of a historical order. Duplicates for
// the same (order_id, item_id) are overwritten rather than appended so
// re-syncing the same order does not double-count items.
func (s *Store) UpsertOrderItem(it OrderItem) error {
	_, err := s.db.Exec(
		`INSERT INTO order_items(order_id, item_id, product_id, name, quantity, quantity_type, price_cents)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(order_id, item_id) DO UPDATE SET
			product_id=excluded.product_id,
			name=excluded.name,
			quantity=excluded.quantity,
			quantity_type=excluded.quantity_type,
			price_cents=excluded.price_cents`,
		it.OrderID, it.ItemID, it.ProductID, it.Name, it.Quantity, it.QuantityType, it.PriceCents,
	)
	return err
}

// UpsertPurchasedItem rolls one observation (one item from one order) into
// the aggregated purchased_items row, incrementing purchase_count and
// refreshing last_purchased_at. Also mirrors into purchased_items_fts.
//
// The count increment is deliberately on ON CONFLICT so repeated sync runs
// against the same order DO NOT inflate counts -- sync logic must only
// call this for newly-seen (order_id, item_id) pairs. The add-time
// resolver calls this once per successful purchase.
func (s *Store) UpsertPurchasedItem(p PurchasedItem, incrementCount bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	inStock := 0
	if p.LastInStock {
		inStock = 1
	}

	countExpr := ""
	if incrementCount {
		countExpr = "purchase_count = purchase_count + 1,"
	}

	_, err = tx.Exec(
		`INSERT INTO purchased_items(
			item_id, retailer_slug, product_id, name, brand, size, category,
			last_purchased_at, first_purchased_at, purchase_count, last_price_cents, last_in_stock)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(item_id, retailer_slug) DO UPDATE SET
			product_id=COALESCE(excluded.product_id, product_id),
			name=COALESCE(NULLIF(excluded.name, ''), name),
			brand=COALESCE(NULLIF(excluded.brand, ''), brand),
			size=COALESCE(NULLIF(excluded.size, ''), size),
			category=COALESCE(NULLIF(excluded.category, ''), category),
			last_purchased_at=MAX(excluded.last_purchased_at, last_purchased_at),
			`+countExpr+`
			last_price_cents=excluded.last_price_cents,
			last_in_stock=excluded.last_in_stock`,
		p.ItemID, p.RetailerSlug, p.ProductID, p.Name, p.Brand, p.Size, p.Category,
		p.LastPurchasedAt.Unix(), p.FirstPurchasedAt.Unix(), max(1, p.PurchaseCount),
		p.LastPriceCents, inStock,
	)
	if err != nil {
		return err
	}

	// FTS mirror: delete + insert (FTS5 upsert dance, same as products_fts).
	_, err = tx.Exec(
		`DELETE FROM purchased_items_fts WHERE item_id = ? AND retailer_slug = ?`,
		p.ItemID, p.RetailerSlug,
	)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO purchased_items_fts(item_id, retailer_slug, name, brand, size, category)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		p.ItemID, p.RetailerSlug, p.Name, p.Brand, p.Size, p.Category,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SearchPurchasedItems runs an FTS5 query against purchased_items_fts filtered
// to one retailer, returning the top N matches ordered by FTS relevance then
// by recency. Returns (nil, nil) on empty match.
func (s *Store) SearchPurchasedItems(query, retailerSlug string, limit int) ([]PurchasedItem, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.Query(
		`SELECT p.item_id, p.retailer_slug, p.product_id, p.name, p.brand, p.size, p.category,
			p.last_purchased_at, p.first_purchased_at, p.purchase_count, p.last_price_cents, p.last_in_stock
		 FROM purchased_items_fts f
		 JOIN purchased_items p ON p.item_id = f.item_id AND p.retailer_slug = f.retailer_slug
		 WHERE f.retailer_slug = ? AND purchased_items_fts MATCH ?
		 ORDER BY bm25(purchased_items_fts), p.last_purchased_at DESC
		 LIMIT ?`,
		retailerSlug, query, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPurchasedItems(rows)
}

// ListPurchasedItems returns top-N purchased items by purchase_count DESC,
// optionally filtered by retailer slug (pass "" for all retailers).
func (s *Store) ListPurchasedItems(retailerSlug string, limit int) ([]PurchasedItem, error) {
	if limit <= 0 {
		limit = 25
	}
	var rows *sql.Rows
	var err error
	if retailerSlug == "" {
		rows, err = s.db.Query(
			`SELECT item_id, retailer_slug, product_id, name, brand, size, category,
				last_purchased_at, first_purchased_at, purchase_count, last_price_cents, last_in_stock
			 FROM purchased_items
			 ORDER BY purchase_count DESC, last_purchased_at DESC
			 LIMIT ?`,
			limit,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT item_id, retailer_slug, product_id, name, brand, size, category,
				last_purchased_at, first_purchased_at, purchase_count, last_price_cents, last_in_stock
			 FROM purchased_items WHERE retailer_slug = ?
			 ORDER BY purchase_count DESC, last_purchased_at DESC
			 LIMIT ?`,
			retailerSlug, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPurchasedItems(rows)
}

func scanPurchasedItems(rows *sql.Rows) ([]PurchasedItem, error) {
	var out []PurchasedItem
	for rows.Next() {
		var p PurchasedItem
		var lastTs, firstTs int64
		var inStock int
		if err := rows.Scan(
			&p.ItemID, &p.RetailerSlug, &p.ProductID, &p.Name, &p.Brand, &p.Size, &p.Category,
			&lastTs, &firstTs, &p.PurchaseCount, &p.LastPriceCents, &inStock,
		); err != nil {
			return nil, err
		}
		p.LastPurchasedAt = time.Unix(lastTs, 0)
		p.FirstPurchasedAt = time.Unix(firstTs, 0)
		p.LastInStock = inStock == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// HistorySyncMeta tracks per-retailer sync status.
type HistorySyncMeta struct {
	RetailerSlug       string
	LastSyncAt         time.Time
	LastSyncStatus     string
	LastSyncError      string
	LastSyncOrderCount int
	LastSyncItemCount  int
	OptedOut           bool
}

// UpsertHistorySyncMeta records the outcome of a sync attempt for one retailer.
func (s *Store) UpsertHistorySyncMeta(m HistorySyncMeta) error {
	optedOut := 0
	if m.OptedOut {
		optedOut = 1
	}
	var lastAt int64
	if !m.LastSyncAt.IsZero() {
		lastAt = m.LastSyncAt.Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO history_sync_meta(
			retailer_slug, last_sync_at, last_sync_status, last_sync_error,
			last_sync_order_count, last_sync_item_count, opted_out)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(retailer_slug) DO UPDATE SET
			last_sync_at=excluded.last_sync_at,
			last_sync_status=excluded.last_sync_status,
			last_sync_error=excluded.last_sync_error,
			last_sync_order_count=excluded.last_sync_order_count,
			last_sync_item_count=excluded.last_sync_item_count,
			opted_out=MAX(excluded.opted_out, opted_out)`,
		m.RetailerSlug, lastAt, m.LastSyncStatus, m.LastSyncError,
		m.LastSyncOrderCount, m.LastSyncItemCount, optedOut,
	)
	return err
}

// GetHistorySyncMeta returns the sync record for a retailer, or nil if
// no sync has ever been attempted there.
func (s *Store) GetHistorySyncMeta(retailerSlug string) (*HistorySyncMeta, error) {
	var m HistorySyncMeta
	var lastAt int64
	var optedOut int
	err := s.db.QueryRow(
		`SELECT retailer_slug, last_sync_at, last_sync_status, last_sync_error,
			last_sync_order_count, last_sync_item_count, opted_out
		 FROM history_sync_meta WHERE retailer_slug = ?`,
		retailerSlug,
	).Scan(&m.RetailerSlug, &lastAt, &m.LastSyncStatus, &m.LastSyncError,
		&m.LastSyncOrderCount, &m.LastSyncItemCount, &optedOut)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastAt > 0 {
		m.LastSyncAt = time.Unix(lastAt, 0)
	}
	m.OptedOut = optedOut == 1
	return &m, nil
}

// ListHistorySyncMeta returns the sync record for every retailer. Used by
// `history stats` and `doctor` to surface global state.
func (s *Store) ListHistorySyncMeta() ([]HistorySyncMeta, error) {
	rows, err := s.db.Query(
		`SELECT retailer_slug, last_sync_at, last_sync_status, last_sync_error,
			last_sync_order_count, last_sync_item_count, opted_out
		 FROM history_sync_meta ORDER BY retailer_slug`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistorySyncMeta
	for rows.Next() {
		var m HistorySyncMeta
		var lastAt int64
		var optedOut int
		if err := rows.Scan(&m.RetailerSlug, &lastAt, &m.LastSyncStatus, &m.LastSyncError,
			&m.LastSyncOrderCount, &m.LastSyncItemCount, &optedOut); err != nil {
			return nil, err
		}
		if lastAt > 0 {
			m.LastSyncAt = time.Unix(lastAt, 0)
		}
		m.OptedOut = optedOut == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountPurchasedItems returns (total, lastPurchasedAt) across all retailers.
// Used by doctor for a one-liner summary.
func (s *Store) CountPurchasedItems() (int, time.Time, error) {
	var n int
	var lastAt sql.NullInt64
	err := s.db.QueryRow(
		`SELECT COUNT(*), MAX(last_purchased_at) FROM purchased_items`,
	).Scan(&n, &lastAt)
	if err != nil {
		return 0, time.Time{}, err
	}
	if !lastAt.Valid {
		return n, time.Time{}, nil
	}
	return n, time.Unix(lastAt.Int64, 0), nil
}

// CountOrders returns the total number of locally-known orders.
func (s *Store) CountOrders() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&n)
	return n, err
}

// MostRecentOrderAt returns the placed_at of the newest known order,
// or zero time if there are no orders. Used for incremental sync.
func (s *Store) MostRecentOrderAt(retailerSlug string) (time.Time, error) {
	var ts sql.NullInt64
	var err error
	if retailerSlug == "" {
		err = s.db.QueryRow(`SELECT MAX(placed_at) FROM orders`).Scan(&ts)
	} else {
		err = s.db.QueryRow(`SELECT MAX(placed_at) FROM orders WHERE retailer_slug = ?`, retailerSlug).Scan(&ts)
	}
	if err != nil {
		return time.Time{}, err
	}
	if !ts.Valid {
		return time.Time{}, nil
	}
	return time.Unix(ts.Int64, 0), nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// DB exposes the underlying *sql.DB for callers that need to run
// ad-hoc queries. Inserted by the sweep-learn-install retrofit;
// the canonical generator template defines an equivalent.
// Callers must not call Close on the returned handle.
func (s *Store) DB() *sql.DB {
	return s.db
}
