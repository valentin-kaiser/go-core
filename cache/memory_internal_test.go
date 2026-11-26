package cache

import (
	"context"
	"testing"
	"time"
)

const (
	clockKey     = "session"
	clockValue   = "value"
	clockTTL     = time.Minute
	clockElapsed = 20 * time.Second
	clockOther   = "other"
)

func newClockCache(t *testing.T) (*MemoryCache, *fakeClock) {
	t.Helper()

	cfg := DefaultConfig()
	cfg.CleanupInterval = 0
	c := NewMemoryCacheWithConfig(cfg)
	clock := newFakeClock()
	c.now = clock.Now
	t.Cleanup(func() {
		err := c.Close()
		if err != nil {
			t.Errorf("closing cache failed: %v", err)
		}
	})

	return c, clock
}

func TestMemoryCache_ExpiryFollowsClock(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, ctx context.Context, c *MemoryCache, wantPresent bool)
	}{
		{
			name: "Get",
			check: func(t *testing.T, ctx context.Context, c *MemoryCache, wantPresent bool) {
				t.Helper()

				var got string
				found, err := c.Get(ctx, clockKey, &got)
				if err != nil {
					t.Fatalf("Get failed: %v", err)
				}
				if found != wantPresent {
					t.Fatalf("Get found = %v, want %v", found, wantPresent)
				}
				if wantPresent && got != clockValue {
					t.Fatalf("Get value = %q, want %q", got, clockValue)
				}
			},
		},
		{
			name: "Exists",
			check: func(t *testing.T, ctx context.Context, c *MemoryCache, wantPresent bool) {
				t.Helper()

				found, err := c.Exists(ctx, clockKey)
				if err != nil {
					t.Fatalf("Exists failed: %v", err)
				}
				if found != wantPresent {
					t.Fatalf("Exists = %v, want %v", found, wantPresent)
				}
			},
		},
		{
			name: "TTL",
			check: func(t *testing.T, ctx context.Context, c *MemoryCache, wantPresent bool) {
				t.Helper()

				ttl, err := c.TTL(ctx, clockKey)
				if wantPresent {
					if err != nil {
						t.Fatalf("TTL failed: %v", err)
					}
					if want := clockTTL - clockElapsed; ttl != want {
						t.Fatalf("TTL = %v, want %v", ttl, want)
					}
					return
				}
				if err == nil {
					t.Fatalf("TTL = %v, want an error for an expired key", ttl)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, clock := newClockCache(t)
			ctx := t.Context()
			err := c.Set(ctx, clockKey, clockValue, clockTTL)
			if err != nil {
				t.Fatalf("Set failed: %v", err)
			}

			clock.Advance(clockElapsed)
			tt.check(t, ctx, c, true)

			clock.Advance(clockTTL)
			tt.check(t, ctx, c, false)
		})
	}
}

func TestMemoryCache_SetTTLUsesClock(t *testing.T) {
	c, clock := newClockCache(t)
	ctx := t.Context()
	err := c.Set(ctx, clockKey, clockValue, clockTTL)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	clock.Advance(clockElapsed)
	err = c.SetTTL(ctx, clockKey, clockTTL)
	if err != nil {
		t.Fatalf("SetTTL failed: %v", err)
	}
	clock.Advance(clockTTL - time.Second)

	found, err := c.Exists(ctx, clockKey)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !found {
		t.Fatal("key expired although SetTTL restarted the TTL")
	}
}

func TestMemoryCache_CleanupRemovesExpired(t *testing.T) {
	c, clock := newClockCache(t)
	ctx := t.Context()
	err := c.Set(ctx, clockKey, clockValue, clockTTL)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	err = c.Set(ctx, clockOther, clockValue, 2*clockTTL)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	c.cleanupExpired()
	if got := c.Size(); got != 2 {
		t.Fatalf("Size before expiry = %d, want 2", got)
	}

	clock.Advance(clockTTL + time.Second)
	c.cleanupExpired()
	if got := c.Size(); got != 1 {
		t.Fatalf("Size after expiry = %d, want 1", got)
	}
	keys := c.Keys()
	if len(keys) != 1 || keys[0] != clockOther {
		t.Fatalf("Keys = %v, want [%s]", keys, clockOther)
	}
}
