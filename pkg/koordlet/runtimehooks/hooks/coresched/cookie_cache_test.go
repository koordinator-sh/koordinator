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
)

func TestCookieCacheEntry(t *testing.T) {
	type args struct {
		initCookieID uint64
		orderedPIDs  []uint32
		setCookieID  uint64
		addPIDs      []uint32
	}
	type expects struct {
		expectInvalid            bool
		expectOrderedPIDsAdded   []uint32
		expectOrderedPIDsDeleted []uint32
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
				orderedPIDs: []uint32{
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
				expectOrderedPIDsAdded: []uint32{
					1,
					2,
					1000,
					1001,
					1010,
					1100,
					2000,
					2002,
				},
				expectOrderedPIDsDeleted: []uint32{
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
				orderedPIDs: []uint32{
					10000,
					10010,
				},
				setCookieID: 200000000,
				addPIDs: []uint32{
					10001,
				},
			},
			expects: expects{
				expectInvalid: false,
				expectOrderedPIDsAdded: []uint32{
					10000,
					10001,
					10010,
				},
				expectOrderedPIDsDeleted: []uint32{
					10000,
					10010,
				},
			},
		},
		{
			name: "valid entry add 1",
			args: args{
				initCookieID: 100000000,
				orderedPIDs: []uint32{
					10,
					1000,
					1001,
					1010,
					1100,
					2000,
					2002,
				},
				setCookieID: 100000000,
				addPIDs: []uint32{
					3,
					1011,
					3000,
				},
			},
			expects: expects{
				expectInvalid: false,
				expectOrderedPIDsAdded: []uint32{
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
				expectOrderedPIDsDeleted: []uint32{
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
				orderedPIDs: []uint32{
					10,
					1000,
					1001,
					1100,
					2000,
					2002,
				},
				setCookieID: 100000000,
				addPIDs: []uint32{
					1001,
					1010,
					3100,
					3000,
				},
			},
			expects: expects{
				expectInvalid: false,
				expectOrderedPIDsAdded: []uint32{
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
				expectOrderedPIDsDeleted: []uint32{
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
			pidsOO := testGetOutOfOrderUint32Slice(tt.args.orderedPIDs)

			// ordered after init
			entry := newCookieCacheEntry(tt.args.initCookieID, pidsOO...)
			assert.NotNil(t, entry)
			assert.Equal(t, tt.args.initCookieID, entry.GetCookieID())
			assert.Equal(t, tt.args.orderedPIDs, entry.GetAllPIDs(), pidsOO)

			// check valid
			assert.Equal(t, tt.expects.expectInvalid, entry.IsEntryInvalid())

			// set cookie
			entry.SetCookieID(tt.args.setCookieID)
			assert.Equal(t, tt.args.setCookieID, entry.GetCookieID())

			// pid exists after init
			if len(tt.args.orderedPIDs) > 0 {
				assert.True(t, entry.HasPID(tt.args.orderedPIDs[0]))
			}

			// ordered after add
			entry.AddPIDs(tt.args.addPIDs...)
			assert.Equal(t, tt.expects.expectOrderedPIDsAdded, entry.GetAllPIDs())

			// pid exists after add
			if len(tt.args.addPIDs) > 0 {
				assert.True(t, entry.HasPID(tt.args.addPIDs[0]))
			}

			// ordered after delete
			entry.DeletePIDs(tt.args.addPIDs...)
			assert.Equal(t, tt.expects.expectOrderedPIDsDeleted, entry.GetAllPIDs())

			// pid not exists after delete
			if len(tt.args.addPIDs) > 0 {
				assert.False(t, entry.HasPID(tt.args.addPIDs[0]))
			}

			// deep copy
			c := entry.DeepCopy()
			assert.Equal(t, c, entry)
		})
	}
}

func testGetOutOfOrderUint32Slice(ss []uint32) []uint32 {
	s := make([]uint32, len(ss))
	copy(s, ss)
	rand.Shuffle(len(s), func(i, j int) {
		s[i], s[j] = s[j], s[i]
	})
	return s
}
