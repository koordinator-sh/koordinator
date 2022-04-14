package cache

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func Test_Cache_Get(t *testing.T) {
	cache := NewCacheDefault()
	cache.gcStarted = true
	cache.items = map[string]item{
		"keyExpire":    {object: "value1", expirationTime: time.Now().Add(-1 * time.Minute)},
		"keyNotExpire": {object: "value2", expirationTime: time.Now().Add(1 * time.Minute)},
	}
	value, found := cache.Get("keyExpire")
	assert.True(t, !found, "value not found", "keyExpire")
	assert.Nil(t, value, "value must be nil", "keyExpire")

	value, found = cache.Get("keyNotExpire")
	assert.True(t, found, "value found", "keyNotExpire")
	assert.Equal(t, "value2", value, "keyNotExpire")
}

func Test_Cache_Set(t *testing.T) {
	cache := NewCacheDefault()
	cache.gcStarted = true
	value, found := cache.Get("key")
	assert.True(t, !found, "value not found")
	assert.Nil(t, value, "value must be nil")

	cache.SetDefault("key", "value")
	value, found = cache.Get("key")
	assert.True(t, found, "value found", "checkSetDefault")
	assert.Equal(t, "value", value, "checkSetDefault")

	cache.Set("key", "value", -1*time.Minute)
	value, found = cache.Get("key")
	assert.True(t, !found, "value not found", "checkSet")
	assert.Nil(t, value, "value must be nil", "checkSet")

}

func Test_gcExpiredCache(t *testing.T) {
	tests := []struct {
		name               string
		initItems          map[string]item
		cache              *Cache
		expectItemsAfterGC map[string]item
	}{
		{
			name: "test_gcExpiredCache_NewCacheDefault",
			initItems: map[string]item{
				"keyNeedExpire": {object: "value1", expirationTime: time.Now().Add(-1 * time.Minute)},
				"keyNotExpire":  {object: "value2", expirationTime: time.Now().Add(time.Minute)},
			},
			cache: NewCacheDefault(),
			expectItemsAfterGC: map[string]item{
				"keyNotExpire": {object: "value2", expirationTime: time.Now().Add(time.Minute)},
			},
		},
		{
			name: "test_gcExpiredCache_NewCache",
			initItems: map[string]item{
				"keyNeedExpire": {object: "value1", expirationTime: time.Now().Add(-1 * time.Minute)},
				"keyNotExpire":  {object: "value2", expirationTime: time.Now().Add(time.Minute)},
			},
			cache: NewCache(time.Minute, time.Minute),
			expectItemsAfterGC: map[string]item{
				"keyNotExpire": {object: "value2", expirationTime: time.Now().Add(time.Minute)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cache.items = tt.initItems
			tt.cache.gcStarted = true
			tt.cache.gcExpiredCache()
			got := tt.cache.items
			assert.Equal(t, len(tt.expectItemsAfterGC), len(got), "checkLen")
			checkValueEqual(t, tt.expectItemsAfterGC, got)
		})
	}
}

func checkValueEqual(t *testing.T, expect, got map[string]item) {
	assert.Equal(t, len(expect), len(got), "checkLen")
	for key, item := range expect {
		gotItem, ok := got[key]
		if !ok {
			assert.True(t, ok, "checkFound", key)
			return
		}
		assert.Equal(t, item.object, gotItem.object, "checkValue", key)
	}
}
