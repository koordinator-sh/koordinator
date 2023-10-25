/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package coresched

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/constraints"
)

func TestCookieCacheEntry(t *testing.T) {
	type args struct {
		initCookieID uint64
		orderedPGIDs []uint32
		setCookieID  uint64
		addPGIDs     []uint32
	}
	type expects struct {
		expectInvalid             bool
		expectOrderedPGIDsAdded   []uint32
		expectOrderedPGIDsDeleted []uint32
	}
	tests := []struct {
		name    string
		args    args
		expects expects
	}{
		{
			name: "empty entry is invalid",
			args: args{
				initCookieID: 0,
			},
			expects: expects{
				expectInvalid: true,
			},
		},
		{
			name: "valid entry",
			args: args{
				initCookieID: 100000000,
				orderedPGIDs: []uint32{
					1,
					2,
					1000,
					1001,
					1010,
					1100,
					2000,
					2002,
				},
				setCookieID: 100000000,
			},
			expects: expects{
				expectInvalid: false,
				expectOrderedPGIDsAdded: []uint32{
					1,
					2,
					1000,
					1001,
					1010,
					1100,
					2000,
					2002,
				},
				expectOrderedPGIDsDeleted: []uint32{
					1,
					2,
					1000,
					1001,
					1010,
					1100,
					2000,
					2002,
				},
			},
		},
		{
			name: "valid entry add",
			args: args{
				initCookieID: 100000000,
				orderedPGIDs: []uint32{
					10000,
					10010,
				},
				setCookieID: 200000000,
				addPGIDs: []uint32{
					10001,
				},
			},
			expects: expects{
				expectInvalid: false,
				expectOrderedPGIDsAdded: []uint32{
					10000,
					10001,
					10010,
				},
				expectOrderedPGIDsDeleted: []uint32{
					10000,
					10010,
				},
			},
		},
		{
			name: "valid entry add 1",
			args: args{
				initCookieID: 100000000,
				orderedPGIDs: []uint32{
					10,
					1000,
					1001,
					1010,
					1100,
					2000,
					2002,
				},
				setCookieID: 100000000,
				addPGIDs: []uint32{
					3,
					1011,
					3000,
				},
			},
			expects: expects{
				expectInvalid: false,
				expectOrderedPGIDsAdded: []uint32{
					3,
					10,
					1000,
					1001,
					1010,
					1011,
					1100,
					2000,
					2002,
					3000,
				},
				expectOrderedPGIDsDeleted: []uint32{
					10,
					1000,
					1001,
					1010,
					1100,
					2000,
					2002,
				},
			},
		},
		{
			name: "valid entry add 2",
			args: args{
				initCookieID: 100000000,
				orderedPGIDs: []uint32{
					10,
					1000,
					1001,
					1100,
					2000,
					2002,
				},
				setCookieID: 100000000,
				addPGIDs: []uint32{
					1001,
					1010,
					3100,
					3000,
				},
			},
			expects: expects{
				expectInvalid: false,
				expectOrderedPGIDsAdded: []uint32{
					10,
					1000,
					1001,
					1010,
					1100,
					2000,
					2002,
					3000,
					3100,
				},
				expectOrderedPGIDsDeleted: []uint32{
					10,
					1000,
					1100,
					2000,
					2002,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgidsOO := testGetOutOfOrderUint32Slice(tt.args.orderedPGIDs)

			// ordered after init
			entry := newCookieCacheEntry(tt.args.initCookieID, pgidsOO...)
			assert.NotNil(t, entry)
			assert.Equal(t, tt.args.initCookieID, entry.GetCookieID())
			assert.Equal(t, tt.args.orderedPGIDs, entry.GetAllPGIDs(), pgidsOO)

			// check valid
			assert.Equal(t, tt.expects.expectInvalid, entry.IsEntryInvalid())

			// set cookie
			entry.SetCookieID(tt.args.setCookieID)
			assert.Equal(t, tt.args.setCookieID, entry.GetCookieID())

			// pgid exists after init
			if len(tt.args.orderedPGIDs) > 0 {
				assert.True(t, entry.HasPGID(tt.args.orderedPGIDs[0]))
			}

			// ordered after add
			entry.AddPGIDs(tt.args.addPGIDs...)
			assert.Equal(t, tt.expects.expectOrderedPGIDsAdded, entry.GetAllPGIDs())

			// pgid exists after add
			if len(tt.args.addPGIDs) > 0 {
				assert.True(t, entry.HasPGID(tt.args.addPGIDs[0]))
			}

			// ordered after delete
			entry.DeletePGIDs(tt.args.addPGIDs...)
			assert.Equal(t, tt.expects.expectOrderedPGIDsDeleted, entry.GetAllPGIDs())

			// pgid not exists after delete
			if len(tt.args.addPGIDs) > 0 {
				assert.False(t, entry.HasPGID(tt.args.addPGIDs[0]))
			}

			// deep copy
			c := entry.DeepCopy()
			assert.Equal(t, c, entry)
		})
	}
}

func TestOrderedMap(t *testing.T) {
	tests := []struct {
		name   string
		testFn func(t *testing.T)
	}{
		{
			name:   "test uint32",
			testFn: testOrderedMap[uint32],
		},
		{
			name:   "test int",
			testFn: testOrderedMap[int],
		},
		{
			name:   "test float64",
			testFn: testOrderedMap[float64],
		},
		{
			name:   "test string",
			testFn: testOrderedMap[string],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.testFn(t)
		})
	}
}

func testOrderedMap[T constraints.Ordered](t *testing.T) {
	m := newOrderedMap[T]()
	assert.NotNil(t, m)

	var e T
	assert.Equal(t, 0, m.Len())
	_, gotExist := m.Get(e)
	assert.False(t, gotExist)
	m.AddAny(e)
	assert.Equal(t, 1, m.Len())
	got, gotExist := m.Get(e)
	assert.True(t, gotExist)
	assert.Equal(t, e, got)
	m.DeleteAny(e)
	assert.Equal(t, 0, m.Len())
	_, gotExist = m.Get(e)
	assert.False(t, gotExist)
	mc := m.DeepCopy()
	assert.Equal(t, m, mc)
}

func testGetOutOfOrderUint32Slice(ss []uint32) []uint32 {
	s := make([]uint32, len(ss))
	copy(s, ss)
	rand.Shuffle(len(s), func(i, j int) {
		s[i], s[j] = s[j], s[i]
	})
	return s
}
