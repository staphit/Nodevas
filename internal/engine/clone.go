package engine

import (
	"reflect"
	"sync"
)

// Clone returns a deep copy of g: no pointer, slice or map is shared with the
// original, so the copy can be edited while other goroutines read g.
//
// The store hands one parsed graph to every reader (see store.LoadGraph), and
// that graph is read-only. A caller that wants to change a loaded graph — to
// save it back, say — clones it first.
//
// The copy is driven by reflection rather than written out field by field, so
// a field added to Graph, Node or UIState later is deep-copied without anyone
// remembering to come back here. Values that hold no references (strings,
// numbers, Position and the like) are copied in one assignment, which keeps it
// several times cheaper than parsing graph.yaml again.
func (g *Graph) Clone() *Graph {
	if g == nil {
		return nil
	}
	out := reflect.New(reflect.TypeFor[Graph]())
	copyDeep(out.Elem(), reflect.ValueOf(g).Elem())
	return out.Interface().(*Graph)
}

// copyDeep sets dst, which must be settable, to a deep copy of src.
func copyDeep(dst, src reflect.Value) {
	if !holdsReferences(src.Type()) {
		dst.Set(src)
		return
	}
	switch src.Kind() {
	case reflect.Pointer:
		if src.IsNil() {
			dst.SetZero()
			return
		}
		fresh := reflect.New(src.Type().Elem())
		copyDeep(fresh.Elem(), src.Elem())
		dst.Set(fresh)
	case reflect.Struct:
		// One assignment copies every field, unexported ones included; the
		// exported fields that hold references are then replaced by copies.
		dst.Set(src)
		for i := range src.NumField() {
			field := src.Type().Field(i)
			if field.IsExported() && holdsReferences(field.Type) {
				copyDeep(dst.Field(i), src.Field(i))
			}
		}
	case reflect.Slice:
		if src.IsNil() {
			dst.SetZero()
			return
		}
		fresh := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		if holdsReferences(src.Type().Elem()) {
			for i := range src.Len() {
				copyDeep(fresh.Index(i), src.Index(i))
			}
		} else {
			reflect.Copy(fresh, src)
		}
		dst.Set(fresh)
	case reflect.Array:
		for i := range src.Len() {
			copyDeep(dst.Index(i), src.Index(i))
		}
	case reflect.Map:
		if src.IsNil() {
			dst.SetZero()
			return
		}
		fresh := reflect.MakeMapWithSize(src.Type(), src.Len())
		keyType, elemType := src.Type().Key(), src.Type().Elem()
		iter := src.MapRange()
		for iter.Next() {
			key := iter.Key()
			if holdsReferences(keyType) {
				copied := reflect.New(keyType).Elem()
				copyDeep(copied, key)
				key = copied
			}
			value := reflect.New(elemType).Elem()
			copyDeep(value, iter.Value())
			fresh.SetMapIndex(key, value)
		}
		dst.Set(fresh)
	case reflect.Interface:
		if src.IsNil() {
			dst.SetZero()
			return
		}
		inner := src.Elem()
		copied := reflect.New(inner.Type()).Elem()
		copyDeep(copied, inner)
		dst.Set(copied)
	default:
		// Channels and functions have no meaningful copy; graphs hold none.
		dst.Set(src)
	}
}

var referenceTypes sync.Map // reflect.Type -> bool

// holdsReferences reports whether a value of t can share memory with a copy
// made by plain assignment.
func holdsReferences(t reflect.Type) bool {
	if cached, ok := referenceTypes.Load(t); ok {
		return cached.(bool)
	}
	// A recursive type reaches itself only through a pointer, slice or map,
	// all of which answer true without looking further, so this terminates.
	var holds bool
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface, reflect.Chan, reflect.Func,
		reflect.UnsafePointer:
		holds = true
	case reflect.Array:
		holds = holdsReferences(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if holdsReferences(t.Field(i).Type) {
				holds = true
				break
			}
		}
	}
	referenceTypes.Store(t, holds)
	return holds
}
