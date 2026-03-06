package retrieval_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/b11902156/rag-gateway/gateway/internal/retrieval"
)

// --- fakes ---

type fakeSections []retrieval.Section

type fakeCache struct {
	data    map[string][]byte
	getCalls int
	setCalls int
}

func newFakeCache() *fakeCache {
	return &fakeCache{data: make(map[string][]byte)}
}

func (f *fakeCache) Get(_ context.Context, key string) ([]byte, error) {
	f.getCalls++
	if v, ok := f.data[key]; ok {
		return v, nil
	}
	return nil, errors.New("miss")
}

func (f *fakeCache) Set(_ context.Context, key string, val []byte, _ time.Duration) error {
	f.setCalls++
	f.data[key] = val
	return nil
}

type fakeInner struct {
	calls    int
	sections []retrieval.Section
	err      error
}

func (f *fakeInner) Retrieve(_ context.Context, _, _ string, _ int32) ([]retrieval.Section, error) {
	f.calls++
	return f.sections, f.err
}

// --- tests ---

var testSections = []retrieval.Section{
	{DocumentID: "doc1", SectionID: "sec1", Content: "hello world", Score: 0.9, TrustTier: "high"},
}

func TestCachedClient_Miss_ThenHit(t *testing.T) {
	cache := newFakeCache()
	inner := &fakeInner{sections: testSections}
	logger := zap.NewNop()

	cc := retrieval.NewCachedClient(inner, cache, 5*time.Minute, logger)

	// First call: cache miss → inner called, result written to cache.
	sections, err := cc.Retrieve(context.Background(), "query1", "tr-1", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sections) != 1 || sections[0].DocumentID != "doc1" {
		t.Fatalf("unexpected sections: %+v", sections)
	}
	if inner.calls != 1 {
		t.Errorf("expected 1 inner call on miss, got %d", inner.calls)
	}
	if cache.setCalls != 1 {
		t.Errorf("expected 1 cache write, got %d", cache.setCalls)
	}

	// Second call with same query: cache hit → inner NOT called again.
	sections2, err := cc.Retrieve(context.Background(), "query1", "tr-2", 5)
	if err != nil {
		t.Fatalf("unexpected error on cache hit: %v", err)
	}
	if len(sections2) != 1 || sections2[0].DocumentID != "doc1" {
		t.Fatalf("unexpected sections on hit: %+v", sections2)
	}
	if inner.calls != 1 {
		t.Errorf("expected inner still called only once (cache hit), got %d", inner.calls)
	}
}

func TestCachedClient_DifferentTopK_DifferentKey(t *testing.T) {
	cache := newFakeCache()
	inner := &fakeInner{sections: testSections}
	logger := zap.NewNop()

	cc := retrieval.NewCachedClient(inner, cache, 5*time.Minute, logger)

	_, _ = cc.Retrieve(context.Background(), "query", "tr-1", 5)
	_, _ = cc.Retrieve(context.Background(), "query", "tr-2", 10) // different topK

	if inner.calls != 2 {
		t.Errorf("expected 2 inner calls (different topK = different key), got %d", inner.calls)
	}
}

func TestCachedClient_InnerError_NotCached(t *testing.T) {
	cache := newFakeCache()
	inner := &fakeInner{err: errors.New("rpc error")}
	logger := zap.NewNop()

	cc := retrieval.NewCachedClient(inner, cache, 5*time.Minute, logger)

	_, err := cc.Retrieve(context.Background(), "query", "tr-1", 5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if cache.setCalls != 0 {
		t.Errorf("expected no cache write on error, got %d", cache.setCalls)
	}
}

func TestCachedClient_CacheWriteFailure_StillReturnsData(t *testing.T) {
	failCache := &failWriteCache{inner: newFakeCache()}
	inner := &fakeInner{sections: testSections}
	logger := zap.NewNop()

	cc := retrieval.NewCachedClient(inner, failCache, 5*time.Minute, logger)

	sections, err := cc.Retrieve(context.Background(), "query", "tr-1", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sections) != 1 {
		t.Errorf("expected 1 section, got %d", len(sections))
	}
}

// failWriteCache always fails Set but succeeds Get.
type failWriteCache struct{ inner *fakeCache }

func (f *failWriteCache) Get(ctx context.Context, key string) ([]byte, error) {
	return f.inner.Get(ctx, key)
}
func (f *failWriteCache) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return errors.New("redis write error")
}
