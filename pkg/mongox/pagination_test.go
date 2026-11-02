package mongox

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestPageQuery_Normalize(t *testing.T) {
	tests := []struct {
		name         string
		input        PageQuery
		wantPage     int64
		wantPageSize int64
	}{
		{
			name:         "zero values get defaults",
			input:        PageQuery{Page: 0, PageSize: 0},
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "negative values get defaults",
			input:        PageQuery{Page: -10, PageSize: -5},
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "oversized pageSize gets capped",
			input:        PageQuery{Page: 2, PageSize: 500},
			wantPage:     2,
			wantPageSize: 200,
		},
		{
			name:         "valid values unchanged",
			input:        PageQuery{Page: 3, PageSize: 50},
			wantPage:     3,
			wantPageSize: 50,
		},
		{
			name:         "boundary pageSize 200 stays",
			input:        PageQuery{Page: 1, PageSize: 200},
			wantPage:     1,
			wantPageSize: 200,
		},
		{
			name:         "boundary pageSize 201 gets capped",
			input:        PageQuery{Page: 1, PageSize: 201},
			wantPage:     1,
			wantPageSize: 200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq := tt.input
			pq.normalize()
			if pq.Page != tt.wantPage {
				t.Errorf("Page = %d, want %d", pq.Page, tt.wantPage)
			}
			if pq.PageSize != tt.wantPageSize {
				t.Errorf("PageSize = %d, want %d", pq.PageSize, tt.wantPageSize)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.URI != "mongodb://localhost:27017" {
		t.Errorf("unexpected default URI: %s", cfg.URI)
	}
	if cfg.Database != "test" {
		t.Errorf("unexpected default Database: %s", cfg.Database)
	}
	if cfg.MaxPoolSize != 100 {
		t.Errorf("unexpected default MaxPoolSize: %d", cfg.MaxPoolSize)
	}
	if cfg.MinPoolSize != 10 {
		t.Errorf("unexpected default MinPoolSize: %d", cfg.MinPoolSize)
	}
}

func TestIsDuplicateKeyError_Unit(t *testing.T) {
	// nil error
	if IsDuplicateKeyError(nil) {
		t.Error("expected false for nil error")
	}

	// regular error
	if IsDuplicateKeyError(ErrNotFound) {
		t.Error("expected false for ErrNotFound")
	}

	// WriteException with duplicate key（InsertOne 场景）
	we := mongo.WriteException{WriteErrors: mongo.WriteErrors{
		{Index: 0, Code: 11000},
	}}
	if !IsDuplicateKeyError(we) {
		t.Error("expected true for WriteException with code 11000")
	}

	// BulkWriteException with duplicate key（InsertMany 场景）
	bwe := mongo.BulkWriteException{WriteErrors: []mongo.BulkWriteError{
		{WriteError: mongo.WriteError{Index: 0, Code: 11000}},
	}}
	if !IsDuplicateKeyError(bwe) {
		t.Error("expected true for BulkWriteException with code 11000")
	}

	// CommandError with duplicate key（事务内场景）
	if !IsDuplicateKeyError(mongo.CommandError{Code: 11000}) {
		t.Error("expected true for CommandError with code 11000")
	}

	// 非重复键错误
	if IsDuplicateKeyError(mongo.WriteException{WriteErrors: mongo.WriteErrors{
		{Index: 0, Code: 121}, // validation error
	}}) {
		t.Error("expected false for non-duplicate-key WriteException")
	}
}

func TestIsDuplicateKeyOnlyError_Unit(t *testing.T) {
	// nil error
	if IsDuplicateKeyOnlyError(nil) {
		t.Error("expected false for nil error")
	}

	// 非服务器错误（网络/编解码等）
	if IsDuplicateKeyOnlyError(errors.New("network error")) {
		t.Error("expected false for non-server error")
	}

	// 纯重复键：WriteException（InsertOne 场景）
	we := mongo.WriteException{WriteErrors: mongo.WriteErrors{
		{Index: 0, Code: 11000},
	}}
	if !IsDuplicateKeyOnlyError(we) {
		t.Error("expected true for pure duplicate key WriteException")
	}

	// 纯重复键：BulkWriteException（InsertMany 并发幂等场景）
	bwe := mongo.BulkWriteException{WriteErrors: []mongo.BulkWriteError{
		{WriteError: mongo.WriteError{Index: 0, Code: 11000}},
		{WriteError: mongo.WriteError{Index: 2, Code: 11001}},
	}}
	if !IsDuplicateKeyOnlyError(bwe) {
		t.Error("expected true for pure duplicate key BulkWriteException")
	}

	// 纯重复键：CommandError（事务内场景）
	if !IsDuplicateKeyOnlyError(mongo.CommandError{Code: 11000}) {
		t.Error("expected true for pure duplicate key CommandError")
	}

	// 混合错误：重复键 + 其他错误，必须上报
	mixed := mongo.BulkWriteException{WriteErrors: []mongo.BulkWriteError{
		{WriteError: mongo.WriteError{Index: 0, Code: 11000}},
		{WriteError: mongo.WriteError{Index: 1, Code: 121}},
	}}
	if IsDuplicateKeyOnlyError(mixed) {
		t.Error("expected false for mixed duplicate key + other error")
	}

	// 非重复键服务器错误
	if IsDuplicateKeyOnlyError(mongo.WriteException{WriteErrors: mongo.WriteErrors{
		{Index: 0, Code: 121},
	}}) {
		t.Error("expected false for non-duplicate-key server error")
	}

	// 写关注错误（WriteConcernError），无重复键
	wc := mongo.WriteException{
		WriteConcernError: &mongo.WriteConcernError{Code: 64},
	}
	if IsDuplicateKeyOnlyError(wc) {
		t.Error("expected false for write concern error")
	}
}

func TestIsTransactionError_Unit(t *testing.T) {
	// nil error
	if IsTransactionError(nil) {
		t.Error("expected false for nil error")
	}

	// regular error
	if IsTransactionError(ErrNotFound) {
		t.Error("expected false for regular error")
	}
}
