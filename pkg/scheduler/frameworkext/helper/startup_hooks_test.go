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

package helper

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Each test in this file mutates the package-global hook registries; they must
// not run in parallel, and every test must start by calling ResetStartupHooks.

func TestRegisterAfterPluginInformersSynced_NilHookIsIgnored(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	RegisterAfterPluginInformersSynced(nil)

	called := false
	RegisterAfterPluginInformersSynced(func(ctx context.Context) error {
		called = true
		return nil
	})

	if err := RunAfterPluginInformersSynced(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.True(t, called, "non-nil hook should still run when a nil hook was filtered out")
}

func TestRegisterAfterAllInformersSynced_NilHookIsIgnored(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	RegisterAfterAllInformersSynced(nil)

	called := false
	RegisterAfterAllInformersSynced(func(ctx context.Context) error {
		called = true
		return nil
	})

	if err := RunAfterAllInformersSynced(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.True(t, called, "non-nil hook should still run when a nil hook was filtered out")
}

func TestRunAfterPluginInformersSynced_PreservesRegistrationOrder(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	var order []int
	for i := 0; i < 5; i++ {
		i := i
		RegisterAfterPluginInformersSynced(func(ctx context.Context) error {
			order = append(order, i)
			return nil
		})
	}

	if err := RunAfterPluginInformersSynced(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, []int{0, 1, 2, 3, 4}, order)
}

func TestRunAfterAllInformersSynced_PreservesRegistrationOrder(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	var order []int
	for i := 0; i < 3; i++ {
		i := i
		RegisterAfterAllInformersSynced(func(ctx context.Context) error {
			order = append(order, i)
			return nil
		})
	}

	if err := RunAfterAllInformersSynced(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, []int{0, 1, 2}, order)
}

func TestRunAfterPluginInformersSynced_ShortCircuitsOnFirstError(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	sentinel := errors.New("boom")

	var callCount int
	RegisterAfterPluginInformersSynced(func(ctx context.Context) error {
		callCount++
		return nil
	})
	RegisterAfterPluginInformersSynced(func(ctx context.Context) error {
		callCount++
		return sentinel
	})
	RegisterAfterPluginInformersSynced(func(ctx context.Context) error {
		callCount++
		return nil
	})

	err := RunAfterPluginInformersSynced(context.Background())
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, 2, callCount, "subsequent hooks should be skipped after the first error")
}

func TestRunAfterAllInformersSynced_ShortCircuitsOnFirstError(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	sentinel := errors.New("boom")

	var callCount int
	RegisterAfterAllInformersSynced(func(ctx context.Context) error {
		callCount++
		return sentinel
	})
	RegisterAfterAllInformersSynced(func(ctx context.Context) error {
		callCount++
		return nil
	})

	err := RunAfterAllInformersSynced(context.Background())
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, callCount, "subsequent hooks should be skipped after the first error")
}

func TestRunAfterPluginInformersSynced_AbortsOnCanceledContext(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	ctx, cancel := context.WithCancel(context.Background())

	var callCount int
	RegisterAfterPluginInformersSynced(func(ctx context.Context) error {
		callCount++
		// Cancel from within the first hook so the next iteration sees ctx.Err().
		cancel()
		return nil
	})
	RegisterAfterPluginInformersSynced(func(ctx context.Context) error {
		callCount++
		return nil
	})

	err := RunAfterPluginInformersSynced(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, callCount, "a canceled ctx must prevent the next hook from running")
}

func TestRunAfterAllInformersSynced_AbortsOnCanceledContext(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	ctx, cancel := context.WithCancel(context.Background())

	var callCount int
	RegisterAfterAllInformersSynced(func(ctx context.Context) error {
		callCount++
		cancel()
		return nil
	})
	RegisterAfterAllInformersSynced(func(ctx context.Context) error {
		callCount++
		return nil
	})

	err := RunAfterAllInformersSynced(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, callCount)
}

func TestRunAfterInformersSynced_EmptyIsNoOp(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	assert.NoError(t, RunAfterPluginInformersSynced(context.Background()))
	assert.NoError(t, RunAfterAllInformersSynced(context.Background()))
}

func TestResetStartupHooks_ClearsBothRegistries(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	RegisterAfterPluginInformersSynced(func(ctx context.Context) error { return errors.New("should-not-run") })
	RegisterAfterAllInformersSynced(func(ctx context.Context) error { return errors.New("should-not-run") })

	ResetStartupHooks()

	assert.NoError(t, RunAfterPluginInformersSynced(context.Background()))
	assert.NoError(t, RunAfterAllInformersSynced(context.Background()))
}

func TestStartupHooks_AfterPluginAndAfterAllAreIndependent(t *testing.T) {
	ResetStartupHooks()
	defer ResetStartupHooks()

	var seenPlugin, seenAll int
	RegisterAfterPluginInformersSynced(func(ctx context.Context) error { seenPlugin++; return nil })
	RegisterAfterAllInformersSynced(func(ctx context.Context) error { seenAll++; return nil })

	// Run plugin phase only; AfterAll hooks must not fire.
	assert.NoError(t, RunAfterPluginInformersSynced(context.Background()))
	assert.Equal(t, 1, seenPlugin)
	assert.Equal(t, 0, seenAll)

	// Run all phase; AfterPlugin must not fire again.
	assert.NoError(t, RunAfterAllInformersSynced(context.Background()))
	assert.Equal(t, 1, seenPlugin)
	assert.Equal(t, 1, seenAll)
}
