package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_RangeValidate(t *testing.T) {

	type args struct {
		name      string
		validator Validate
		value     *int64
		expect    bool
	}

	tests := []args{
		{
			name:      "test_validate_nil",
			validator: &RangeValidator{min: 0, max: 100, name: "wmarkRatio"},
			value:     nil,
			expect:    false,
		},
		{
			name:      "test_validate_invalid",
			validator: &RangeValidator{min: 0, max: 100, name: "wmarkRatio"},
			value:     Int64Ptr(120),
			expect:    false,
		},
		{
			name:      "test_validate_valid_min",
			validator: &RangeValidator{min: 0, max: 100, name: "wmarkRatio"},
			value:     Int64Ptr(0),
			expect:    true,
		},
		{
			name:      "test_validate_valid_max",
			validator: &RangeValidator{min: 0, max: 100, name: "wmarkRatio"},
			value:     Int64Ptr(100),
			expect:    true,
		},
		{
			name:      "test_validate_valid",
			validator: &RangeValidator{min: 0, max: 100, name: "wmarkRatio"},
			value:     Int64Ptr(20),
			expect:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := tt.validator.Validate(tt.value)
			assert.Equal(t, tt.expect, got)
		})
	}
}
