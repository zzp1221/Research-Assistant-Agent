package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"research-assistant-backend/internal/models"
)

// clonePlanGraph copies the entire model, including fields added after the
// original store implementation. Dynamic values retain their concrete types.
func clonePlanGraph(plan *models.PlanGraph) (*models.PlanGraph, error) {
	return cloneValue(plan)
}

func clonePlanEvent(event models.PlanEvent) (models.PlanEvent, error) {
	return cloneValue(event)
}

func cloneValue[T any](input T) (T, error) {
	var zero T
	value := reflect.ValueOf(input)
	if !value.IsValid() {
		return zero, nil
	}
	cloned, err := (&valueCloner{active: make(map[cloneReference]bool)}).copy(value, "$", 0)
	if err != nil {
		return zero, err
	}
	return cloned.Interface().(T), nil
}

// validateJSONValue keeps the memory and file stores' accepted value domain
// consistent. Validation does not replace the type-preserving copy above.
func validateJSONValue(value any) error {
	if _, err := json.Marshal(value); err != nil {
		return fmt.Errorf("encode store value: %w", err)
	}
	return nil
}

type cloneReference struct {
	typeOf reflect.Type
	ptr    uintptr
	length int
}

type valueCloner struct {
	active map[cloneReference]bool
}

var timeType = reflect.TypeOf(time.Time{})

const maxCloneDepth = 1024

func (c *valueCloner) copy(value reflect.Value, path string, depth int) (reflect.Value, error) {
	if depth > maxCloneDepth {
		return reflect.Value{}, fmt.Errorf("clone %s: nesting limit exceeded", path)
	}
	if value.Type() == timeType {
		// time.Time is a value type. Its internal Location is immutable.
		return value, nil
	}

	switch value.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return value, nil
	case reflect.Interface:
		out := reflect.New(value.Type()).Elem()
		if value.IsNil() {
			return out, nil
		}
		child, err := c.copy(value.Elem(), path, depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		out.Set(child)
		return out, nil
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		leave, err := c.enter(value, path)
		if err != nil {
			return reflect.Value{}, err
		}
		defer leave()
		child, err := c.copy(value.Elem(), path+"*", depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(child)
		if out.Type() != value.Type() {
			out = out.Convert(value.Type())
		}
		return out, nil
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		leave, err := c.enter(value, path)
		if err != nil {
			return reflect.Value{}, err
		}
		defer leave()
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key := iter.Key()
			// JSON maps use scalar keys. Pointer/object keys have identity
			// semantics and are not accepted by the plan store.
			switch key.Kind() {
			case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			default:
				return reflect.Value{}, fmt.Errorf("clone %s: unsupported map key %s", path, key.Type())
			}
			child, err := c.copy(iter.Value(), path+"["+fmt.Sprint(key.Interface())+"]", depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(key, child)
		}
		return out, nil
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type()), nil
		}
		leave, err := c.enter(value, path)
		if err != nil {
			return reflect.Value{}, err
		}
		defer leave()
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		if cloneElementHasValueSemantics(value.Type().Elem()) {
			// RawMessage and other large scalar slices need one allocation and
			// a bulk copy rather than reflection and path allocation per byte.
			reflect.Copy(out, value)
			return out, nil
		}
		for i := 0; i < value.Len(); i++ {
			child, err := c.copy(value.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(child)
		}
		return out, nil
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			child, err := c.copy(value.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(child)
		}
		return out, nil
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.PkgPath != "" {
				return reflect.Value{}, fmt.Errorf("clone %s.%s: unsupported unexported field", path, field.Name)
			}
			child, err := c.copy(value.Field(i), path+"."+field.Name, depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(i).Set(child)
		}
		return out, nil
	default:
		return reflect.Value{}, fmt.Errorf("clone %s: unsupported value type %s", path, value.Type())
	}
}

func cloneElementHasValueSemantics(element reflect.Type) bool {
	if element == timeType {
		return true
	}
	switch element.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func (c *valueCloner) enter(value reflect.Value, path string) (func(), error) {
	ref := cloneReference{typeOf: value.Type(), ptr: value.Pointer()}
	if value.Kind() == reflect.Slice {
		ref.length = value.Len()
	}
	if c.active[ref] {
		return nil, fmt.Errorf("clone %s: circular reference", path)
	}
	c.active[ref] = true
	return func() { delete(c.active, ref) }, nil
}
