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

package slocontroller

import (
	"reflect"
	"testing"
)

const testKey = "colocation-config"

func TestPlanSLOConfigData(t *testing.T) {
	tests := []struct {
		name string
		data map[string]string
		want sloConfigAction
	}{
		{
			// The branch this whole helper exists for. The old code noticed
			// Data == nil, set needUpdate, recorded nothing to roll back, and
			// then wrote into the nil map: "assignment to entry in nil map".
			name: "nil data is initialised and restored to nil",
			data: nil,
			want: sloConfigAction{
				needUpdate: true,
				newData:    map[string]string{testKey: "wanted"},
				restoreNil: true,
			},
		},
		{
			// An empty map is not the same as no map: writing into it is fine,
			// so cleanup must drop the key rather than blank out Data.
			name: "empty data gets the key and cleanup removes it",
			data: map[string]string{},
			want: sloConfigAction{
				needUpdate:  true,
				newData:     map[string]string{testKey: "wanted"},
				restoreData: map[string]string{},
				removeKeys:  []string{testKey},
			},
		},
		{
			name: "different value is overwritten and the old one restored",
			data: map[string]string{testKey: "old"},
			want: sloConfigAction{
				needUpdate:  true,
				newData:     map[string]string{testKey: "wanted"},
				restoreData: map[string]string{testKey: "old"},
			},
		},
		{
			name: "matching value needs no update and no rollback",
			data: map[string]string{testKey: "wanted"},
			want: sloConfigAction{},
		},
		{
			// Keys the spec does not own must survive both the write and the
			// rollback, so they are copied rather than replaced.
			name: "unrelated keys are preserved",
			data: map[string]string{testKey: "old", "other-config": "keep"},
			want: sloConfigAction{
				needUpdate:  true,
				newData:     map[string]string{testKey: "wanted", "other-config": "keep"},
				restoreData: map[string]string{testKey: "old"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planSLOConfigData(tt.data, map[string]string{testKey: "wanted"})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("planSLOConfigData() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestPlanSLOConfigDataMultipleKeys covers the cpunormalization shape, which
// sets two keys at once. One key already matching must not stop the other from
// being written, and only the keys that existed get restored.
func TestPlanSLOConfigDataMultipleKeys(t *testing.T) {
	const otherKey = "cpu-normalization-config"

	t.Run("nil data with two keys", func(t *testing.T) {
		got := planSLOConfigData(nil, map[string]string{testKey: "a", otherKey: "b"})

		if !got.needUpdate || !got.restoreNil {
			t.Fatalf("nil data should need an update and restore to nil, got %+v", got)
		}
		if got.newData[testKey] != "a" || got.newData[otherKey] != "b" {
			t.Errorf("newData = %v, want both keys set", got.newData)
		}
	})

	t.Run("one key matches, the other does not", func(t *testing.T) {
		data := map[string]string{testKey: "a"}

		got := planSLOConfigData(data, map[string]string{testKey: "a", otherKey: "b"})

		if !got.needUpdate {
			t.Fatal("a differing second key should still need an update")
		}
		if got.newData[otherKey] != "b" {
			t.Errorf("second key was not written: %v", got.newData)
		}
		// testKey existed and is restored; otherKey did not and is removed.
		if got.restoreData[testKey] != "a" {
			t.Errorf("restoreData = %v, want the existing value kept", got.restoreData)
		}
		if len(got.removeKeys) != 1 || got.removeKeys[0] != otherKey {
			t.Errorf("removeKeys = %v, want only the key that was absent", got.removeKeys)
		}
	})

	t.Run("both keys already match", func(t *testing.T) {
		data := map[string]string{testKey: "a", otherKey: "b"}

		got := planSLOConfigData(data, map[string]string{testKey: "a", otherKey: "b"})

		if got.needUpdate {
			t.Errorf("no update should be needed, got %+v", got)
		}
	})
}

// TestPlanSLOConfigDataDoesNotMutateInput pins that the caller's ConfigMap data
// is left alone. The plan is built before the Update call, and a spec that had
// its own copy changed underneath it would roll back to the wrong value.
func TestPlanSLOConfigDataDoesNotMutateInput(t *testing.T) {
	data := map[string]string{testKey: "old", "other-config": "keep"}

	action := planSLOConfigData(data, map[string]string{testKey: "wanted"})

	if data[testKey] != "old" {
		t.Errorf("input was mutated: %s = %q, want %q", testKey, data[testKey], "old")
	}
	if action.newData[testKey] != "wanted" {
		t.Errorf("newData did not take the wanted value, got %q", action.newData[testKey])
	}
	// Writing through the returned map must not reach the caller's map either.
	action.newData["other-config"] = "changed"
	if data["other-config"] != "keep" {
		t.Errorf("newData aliases the input map: other-config = %q", data["other-config"])
	}
}

// TestPlanSLOConfigDataNilNeverWritesNilMap is the regression guard for the
// panic itself: whatever else changes, the plan for a nil Data must hand back a
// map that can be written to.
func TestPlanSLOConfigDataNilNeverWritesNilMap(t *testing.T) {
	action := planSLOConfigData(nil, map[string]string{testKey: "wanted"})

	if !action.needUpdate {
		t.Fatal("nil data should need an update")
	}
	if action.newData == nil {
		t.Fatal("newData is nil; assigning into it would panic, which is the bug this guards")
	}
	// The assignment that used to panic.
	action.newData["another"] = "value"
}
