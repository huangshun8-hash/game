//go:build integration

package mongox

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// testDoc is a simple document type used across tests.
type testDoc struct {
	ID    bson.ObjectID `bson:"_id,omitempty"`
	Name  string        `bson:"name"`
	Age   int           `bson:"age"`
	Score float64       `bson:"score"`
}

// getTestURI reads the MongoDB URI from the environment or falls back to localhost.
func getTestURI() string {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	return uri
}

// setupTestClient creates a connected client and a unique test database.
// Returns the client and a cleanup function that drops the database and disconnects.
func setupTestClient(t *testing.T) (*Client, func()) {
	t.Helper()

	ctx := context.Background()
	dbName := "mongox_test_" + bson.NewObjectID().Hex()[:8]

	client, err := NewClient(ctx, &Config{
		URI:            getTestURI(),
		Database:       dbName,
		ConnectTimeout: 5 * time.Second,
		PingTimeout:    3 * time.Second,
		MaxPoolSize:    10,
		MinPoolSize:    1,
		MaxIdleTime:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create test client: %v", err)
	}

	cleanup := func() {
		_ = client.Database().Drop(ctx)
		_ = client.Close(ctx)
	}

	return client, cleanup
}

func TestNewClient_Success(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	if client.Raw() == nil {
		t.Fatal("expected non-nil raw client")
	}
	if client.Database() == nil {
		t.Fatal("expected non-nil database")
	}
}

func TestNewClient_InvalidURI(t *testing.T) {
	ctx := context.Background()
	_, err := NewClient(ctx, &Config{
		URI:            "mongodb://invalid-host-that-does-not-exist:99999",
		Database:       "test",
		ConnectTimeout: 1 * time.Second,
		PingTimeout:    1 * time.Second,
		MaxPoolSize:    1,
		MinPoolSize:    1,
		MaxIdleTime:    5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error for invalid URI, got nil")
	}
}

func TestClient_Ping(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}

func TestClient_DB(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	// Empty name returns default database
	db := client.DB("")
	if db.Name() != client.Database().Name() {
		t.Fatalf("expected default DB name %q, got %q", client.Database().Name(), db.Name())
	}

	// Non-empty name returns the named database
	otherDB := client.DB("other_db")
	if otherDB.Name() != "other_db" {
		t.Fatalf("expected DB name 'other_db', got %q", otherDB.Name())
	}
}

func TestClient_Collection(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	coll := client.Collection("test_coll")
	if coll.Name() != "test_coll" {
		t.Fatalf("expected collection name 'test_coll', got %q", coll.Name())
	}
}

// --- CRUD Tests ---

func TestInsertOne_And_FindByID(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	doc := testDoc{Name: "Alice", Age: 30, Score: 95.5}
	id, err := InsertOne(ctx, coll, &doc)
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}
	if id.IsZero() {
		t.Fatal("expected non-zero inserted ID")
	}

	// Find by ID
	found, err := FindByID[testDoc](ctx, coll, id)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if found.Name != "Alice" || found.Age != 30 {
		t.Fatalf("unexpected document: %+v", found)
	}
}

func TestFindOne_NotFound(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	_, err := FindOne[testDoc](ctx, coll, bson.D{{Key: "name", Value: "nonexistent"}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestInsertMany_And_FindMany(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	docs := []any{
		&testDoc{Name: "Bob", Age: 25, Score: 88.0},
		&testDoc{Name: "Charlie", Age: 35, Score: 92.0},
		&testDoc{Name: "Diana", Age: 28, Score: 76.0},
	}
	ids, err := InsertMany(ctx, coll, docs)
	if err != nil {
		t.Fatalf("InsertMany failed: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 IDs, got %d", len(ids))
	}

	// FindMany with filter
	results, err := FindMany[testDoc](ctx, coll, bson.D{{Key: "age", Value: bson.D{{Key: "$gte", Value: 28}}}})
	if err != nil {
		t.Fatalf("FindMany failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestUpdateOne_And_UpdateByID(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	doc := testDoc{Name: "Eve", Age: 22, Score: 60.0}
	id, err := InsertOne(ctx, coll, &doc)
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// UpdateByID
	modified, err := UpdateByID(ctx, coll, id, bson.D{{Key: "$set", Value: bson.D{{Key: "score", Value: 99.0}}}})
	if err != nil {
		t.Fatalf("UpdateByID failed: %v", err)
	}
	if modified != 1 {
		t.Fatalf("expected 1 modified, got %d", modified)
	}

	// Verify
	found, err := FindByID[testDoc](ctx, coll, id)
	if err != nil {
		t.Fatalf("FindByID failed: %v", err)
	}
	if found.Score != 99.0 {
		t.Fatalf("expected score 99.0, got %f", found.Score)
	}
}

func TestUpdateMany(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	docs := []any{
		&testDoc{Name: "A", Age: 20, Score: 50.0},
		&testDoc{Name: "B", Age: 20, Score: 60.0},
		&testDoc{Name: "C", Age: 30, Score: 70.0},
	}
	_, err := InsertMany(ctx, coll, docs)
	if err != nil {
		t.Fatalf("InsertMany failed: %v", err)
	}

	// Update all age=20 docs
	modified, err := UpdateMany(ctx, coll, bson.D{{Key: "age", Value: 20}}, bson.D{{Key: "$set", Value: bson.D{{Key: "score", Value: 100.0}}}})
	if err != nil {
		t.Fatalf("UpdateMany failed: %v", err)
	}
	if modified != 2 {
		t.Fatalf("expected 2 modified, got %d", modified)
	}
}

func TestUpsertOne(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	// Upsert creates new document
	filter := bson.D{{Key: "name", Value: "Frank"}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "age", Value: 40}, {Key: "score", Value: 80.0}}}}
	result, err := UpsertOne(ctx, coll, filter, update)
	if err != nil {
		t.Fatalf("UpsertOne failed: %v", err)
	}
	if result.UpsertedCount != 1 {
		t.Fatalf("expected 1 upserted, got %d", result.UpsertedCount)
	}

	// Upsert updates existing document
	update2 := bson.D{{Key: "$set", Value: bson.D{{Key: "score", Value: 90.0}}}}
	result2, err := UpsertOne(ctx, coll, filter, update2)
	if err != nil {
		t.Fatalf("UpsertOne (update) failed: %v", err)
	}
	if result2.ModifiedCount != 1 {
		t.Fatalf("expected 1 modified, got %d", result2.ModifiedCount)
	}
}

func TestDeleteOne_And_DeleteByID(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	doc := testDoc{Name: "Gina", Age: 26, Score: 70.0}
	id, err := InsertOne(ctx, coll, &doc)
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// DeleteByID
	deleted, err := DeleteByID(ctx, coll, id)
	if err != nil {
		t.Fatalf("DeleteByID failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// Verify it's gone
	_, err = FindByID[testDoc](ctx, coll, id)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestDeleteMany(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	docs := []any{
		&testDoc{Name: "X", Age: 18, Score: 50.0},
		&testDoc{Name: "Y", Age: 18, Score: 55.0},
		&testDoc{Name: "Z", Age: 40, Score: 80.0},
	}
	_, err := InsertMany(ctx, coll, docs)
	if err != nil {
		t.Fatalf("InsertMany failed: %v", err)
	}

	deleted, err := DeleteMany(ctx, coll, bson.D{{Key: "age", Value: 18}})
	if err != nil {
		t.Fatalf("DeleteMany failed: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", deleted)
	}
}

func TestCount_And_Exists(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("users")

	docs := []any{
		&testDoc{Name: "P1", Age: 50, Score: 70.0},
		&testDoc{Name: "P2", Age: 50, Score: 80.0},
	}
	_, err := InsertMany(ctx, coll, docs)
	if err != nil {
		t.Fatalf("InsertMany failed: %v", err)
	}

	count, err := Count(ctx, coll, bson.D{{Key: "age", Value: 50}})
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}

	exists, err := Exists(ctx, coll, bson.D{{Key: "name", Value: "P1"}})
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Fatal("expected Exists to return true")
	}

	exists, err = Exists(ctx, coll, bson.D{{Key: "name", Value: "nobody"}})
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Fatal("expected Exists to return false for nonexistent document")
	}
}

// --- Transaction Tests ---

func TestRunTransaction_Success(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("accounts")

	// Insert two accounts
	_, err := InsertOne(ctx, coll, &testDoc{Name: "Acc1", Score: 100.0})
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}
	_, err = InsertOne(ctx, coll, &testDoc{Name: "Acc2", Score: 50.0})
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// Transfer 30 from Acc1 to Acc2 in a transaction
	err = client.RunTransaction(ctx, func(sc context.Context) error {
		_, e := UpdateOne(sc, coll, bson.D{{Key: "name", Value: "Acc1"}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "score", Value: -30.0}}}})
		if e != nil {
			return e
		}
		_, e = UpdateOne(sc, coll, bson.D{{Key: "name", Value: "Acc2"}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "score", Value: 30.0}}}})
		return e
	})
	if err != nil {
		t.Fatalf("RunTransaction failed: %v", err)
	}

	// Verify balances
	acc1, err := FindOne[testDoc](ctx, coll, bson.D{{Key: "name", Value: "Acc1"}})
	if err != nil {
		t.Fatalf("FindOne failed: %v", err)
	}
	if acc1.Score != 70.0 {
		t.Fatalf("expected Acc1 score 70.0, got %f", acc1.Score)
	}

	acc2, err := FindOne[testDoc](ctx, coll, bson.D{{Key: "name", Value: "Acc2"}})
	if err != nil {
		t.Fatalf("FindOne failed: %v", err)
	}
	if acc2.Score != 80.0 {
		t.Fatalf("expected Acc2 score 80.0, got %f", acc2.Score)
	}
}

func TestRunTransaction_Abort(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("accounts")

	_, err := InsertOne(ctx, coll, &testDoc{Name: "AccA", Score: 100.0})
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// Transaction that fails should abort changes
	errBoom := errors.New("insufficient balance")
	err = client.RunTransaction(ctx, func(sc context.Context) error {
		_, e := UpdateOne(sc, coll, bson.D{{Key: "name", Value: "AccA"}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "score", Value: -200.0}}}})
		if e != nil {
			return e
		}
		return errBoom // abort
	})
	if err == nil {
		t.Fatal("expected transaction error, got nil")
	}

	// Verify the balance is unchanged (transaction was aborted)
	acc, err := FindOne[testDoc](ctx, coll, bson.D{{Key: "name", Value: "AccA"}})
	if err != nil {
		t.Fatalf("FindOne failed: %v", err)
	}
	if acc.Score != 100.0 {
		t.Fatalf("expected AccA score 100.0 (unchanged), got %f", acc.Score)
	}
}

func TestRunTransactionWithResult(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("counters")

	_, err := InsertOne(ctx, coll, &testDoc{Name: "counter1", Score: 0})
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// Transaction that returns a result
	result, err := RunTransactionWithResult[string](client, ctx, func(sc context.Context) (string, error) {
		_, e := UpdateOne(sc, coll, bson.D{{Key: "name", Value: "counter1"}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "score", Value: 1}}}})
		if e != nil {
			return "", e
		}
		return "incremented", nil
	})
	if err != nil {
		t.Fatalf("RunTransactionWithResult failed: %v", err)
	}
	if result != "incremented" {
		t.Fatalf("expected result 'incremented', got %q", result)
	}
}

// --- Pagination Tests ---

func TestPaginate(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("items")

	// Insert 25 documents
	docs := make([]any, 25)
	for i := range 25 {
		docs[i] = &testDoc{Name: "item", Age: i, Score: float64(i * 10)}
	}
	_, err := InsertMany(ctx, coll, docs)
	if err != nil {
		t.Fatalf("InsertMany failed: %v", err)
	}

	// Page 1, size 10
	page1, err := Paginate[testDoc](ctx, coll, bson.D{}, PageQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Paginate page 1 failed: %v", err)
	}
	if page1.Total != 25 {
		t.Fatalf("expected total 25, got %d", page1.Total)
	}
	if page1.TotalPages != 3 {
		t.Fatalf("expected 3 total pages, got %d", page1.TotalPages)
	}
	if len(page1.Items) != 10 {
		t.Fatalf("expected 10 items on page 1, got %d", len(page1.Items))
	}

	// Page 3, size 10 (should have 5 items)
	page3, err := Paginate[testDoc](ctx, coll, bson.D{}, PageQuery{Page: 3, PageSize: 10})
	if err != nil {
		t.Fatalf("Paginate page 3 failed: %v", err)
	}
	if len(page3.Items) != 5 {
		t.Fatalf("expected 5 items on page 3, got %d", len(page3.Items))
	}
}

func TestPaginate_Normalize(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("items")

	_, err := InsertOne(ctx, coll, &testDoc{Name: "single", Age: 1})
	if err != nil {
		t.Fatalf("InsertOne failed: %v", err)
	}

	// Invalid page/pageSize should be normalized
	page, err := Paginate[testDoc](ctx, coll, bson.D{}, PageQuery{Page: -5, PageSize: 0})
	if err != nil {
		t.Fatalf("Paginate with invalid params failed: %v", err)
	}
	if page.Page != 1 {
		t.Fatalf("expected page normalized to 1, got %d", page.Page)
	}
	if page.PageSize != 20 {
		t.Fatalf("expected pageSize normalized to 20, got %d", page.PageSize)
	}

	// Oversized pageSize should be capped
	page2, err := Paginate[testDoc](ctx, coll, bson.D{}, PageQuery{Page: 1, PageSize: 500})
	if err != nil {
		t.Fatalf("Paginate with large pageSize failed: %v", err)
	}
	if page2.PageSize != 200 {
		t.Fatalf("expected pageSize capped to 200, got %d", page2.PageSize)
	}
}

func TestPaginateCursor(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("items")

	// Insert 10 documents
	docs := make([]any, 10)
	for i := range 10 {
		docs[i] = &testDoc{Name: "cursor_item", Age: i, Score: float64(i)}
	}
	_, err := InsertMany(ctx, coll, docs)
	if err != nil {
		t.Fatalf("InsertMany failed: %v", err)
	}

	// First page
	result1, err := PaginateCursor[testDoc](ctx, coll, bson.D{}, CursorPageQuery{Limit: 5})
	if err != nil {
		t.Fatalf("PaginateCursor page 1 failed: %v", err)
	}
	if len(result1.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result1.Items))
	}
	if !result1.HasMore {
		t.Fatal("expected HasMore to be true")
	}
	if result1.NextCursor == "" {
		t.Fatal("expected non-empty NextCursor")
	}

	// Second page
	result2, err := PaginateCursor[testDoc](ctx, coll, bson.D{}, CursorPageQuery{Cursor: result1.NextCursor, Limit: 5})
	if err != nil {
		t.Fatalf("PaginateCursor page 2 failed: %v", err)
	}
	if len(result2.Items) != 5 {
		t.Fatalf("expected 5 items on page 2, got %d", len(result2.Items))
	}
	if result2.HasMore {
		t.Fatal("expected HasMore to be false on last page")
	}
}

func TestPaginateCursor_InvalidCursor(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("items")

	_, err := PaginateCursor[testDoc](ctx, coll, bson.D{}, CursorPageQuery{Cursor: "invalid_hex", Limit: 10})
	if err == nil {
		t.Fatal("expected error for invalid cursor, got nil")
	}
}

// --- Index Tests ---

func TestEnsureIndexes(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()

	indexes := []IndexDef{
		{
			Collection: "users",
			Keys:       bson.D{{Key: "name", Value: 1}},
			Unique:     true,
			Name:       "idxUsersName",
		},
		{
			Collection: "users",
			Keys:       bson.D{{Key: "age", Value: 1}, {Key: "score", Value: -1}},
			Name:       "idxUsersAgeScore",
		},
		{
			Collection:         "sessions",
			Keys:               bson.D{{Key: "expiresAt", Value: 1}},
			ExpireAfterSeconds: 3600,
			Name:               "idxSessionsTtl",
		},
	}

	err := client.EnsureIndexes(ctx, indexes)
	if err != nil {
		t.Fatalf("EnsureIndexes failed: %v", err)
	}

	// Verify by listing indexes on the users collection
	cursor, err := client.Collection("users").Indexes().List(ctx)
	if err != nil {
		t.Fatalf("List indexes failed: %v", err)
	}
	defer cursor.Close(ctx)

	var indexDocs []bson.M
	if err = cursor.All(ctx, &indexDocs); err != nil {
		t.Fatalf("decode index list failed: %v", err)
	}

	// Should have 3 indexes: _id + our 2
	if len(indexDocs) != 3 {
		t.Fatalf("expected 3 indexes on users, got %d", len(indexDocs))
	}

	// Running EnsureIndexes again should be idempotent
	err = client.EnsureIndexes(ctx, indexes)
	if err != nil {
		t.Fatalf("EnsureIndexes (idempotent) failed: %v", err)
	}
}

// --- Duplicate Key Tests ---

func TestIsDuplicateKeyError(t *testing.T) {
	client, cleanup := setupTestClient(t)
	defer cleanup()

	ctx := context.Background()
	coll := client.Collection("unique_items")

	// Create unique index
	err := client.EnsureIndexes(ctx, []IndexDef{
		{Collection: "unique_items", Keys: bson.D{{Key: "name", Value: 1}}, Unique: true},
	})
	if err != nil {
		t.Fatalf("EnsureIndexes failed: %v", err)
	}

	_, err = InsertOne(ctx, coll, &testDoc{Name: "dup", Age: 1})
	if err != nil {
		t.Fatalf("first InsertOne failed: %v", err)
	}

	// Insert duplicate
	_, err = InsertOne(ctx, coll, &testDoc{Name: "dup", Age: 2})
	if err == nil {
		t.Fatal("expected duplicate key error, got nil")
	}
	if !IsDuplicateKeyError(err) {
		t.Fatalf("expected IsDuplicateKeyError to return true, err: %v", err)
	}
}

func TestIsDuplicateKeyError_NilError(t *testing.T) {
	if IsDuplicateKeyError(nil) {
		t.Fatal("expected false for nil error")
	}
}

func TestIsTransactionError_NilError(t *testing.T) {
	if IsTransactionError(nil) {
		t.Fatal("expected false for nil error")
	}
}

func TestIsTransactionError_NonCommandError(t *testing.T) {
	if IsTransactionError(errors.New("random error")) {
		t.Fatal("expected false for non-CommandError")
	}
}

func TestIsTransactionError_CommandErrorWithoutLabel(t *testing.T) {
	cmdErr := mongo.CommandError{Code: 100, Message: "some error"}
	if IsTransactionError(cmdErr) {
		t.Fatal("expected false for CommandError without TransientTransactionError label")
	}
}
