package system

import "fmt"

type Validate interface {
	Validate(value *int64) (isValid bool, msg string)
}

type RangeValidator struct {
	name string
	max  int64
	min  int64
}

func (r *RangeValidator) Validate(value *int64) (isValid bool, msg string) {
	isValid = false
	if value == nil {
		msg = fmt.Sprintf("Value is not valid!%s value is nil!", r.name)
		return
	}
	isValid = *value >= r.min && *value <= r.max
	if !isValid {
		msg = fmt.Sprintf("%s Value(%d) is not valid!min:%d,max:%d", r.name, *value, r.min, r.max)
	}
	return
}

// Int64Ptr returns a int64 pointer for given value
func Int64Ptr(v int64) *int64 {
	return &v
}
