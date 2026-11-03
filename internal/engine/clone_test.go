package engine

import (
	"fmt"
	"reflect"
	"testing"
)

// fillValue sets every reachable field of v to a non-zero value, so a clone
// test exercises fields added after it was written as well.
func fillValue(v reflect.Value, depth int) {
	if depth > 6 {
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(fmt.Sprintf("s%d", depth))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(depth + 1))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(depth + 1))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(depth) + 0.5)
	case reflect.Pointer:
		elem := reflect.New(v.Type().Elem())
		fillValue(elem.Elem(), depth+1)
		v.Set(elem)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillValue(v.Field(i), depth+1)
			}
		}
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := range 2 {
			fillValue(s.Index(i), depth+1)
		}
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		for i := range 2 {
			key := reflect.New(v.Type().Key()).Elem()
			fillValue(key, depth+1)
			if key.Kind() == reflect.String {
				key.SetString(fmt.Sprintf("k%d", i))
			}
			val := reflect.New(v.Type().Elem()).Elem()
			fillValue(val, depth+1)
			m.SetMapIndex(key, val)
		}
		v.Set(m)
	case reflect.Interface:
		// What YAML decodes into a map[string]any: nested maps and lists.
		v.Set(reflect.ValueOf(map[string]any{"nested": []any{"x", map[string]any{"y": 1}}}))
	}
}

// assertNoAliasing walks a and b in step and fails on any pointer, map or
// slice the two share: a write through the clone must never reach the source.
func assertNoAliasing(t *testing.T, path string, a, b reflect.Value) {
	t.Helper()
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Fatalf("%s: clone shares pointer with source", path)
		}
		assertNoAliasing(t, path, a.Elem(), b.Elem())
	case reflect.Struct:
		for i := range a.NumField() {
			if a.Type().Field(i).IsExported() {
				assertNoAliasing(t, path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i))
			}
		}
	case reflect.Slice:
		if a.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Fatalf("%s: clone shares slice backing array with source", path)
		}
		for i := range a.Len() {
			assertNoAliasing(t, fmt.Sprintf("%s[%d]", path, i), a.Index(i), b.Index(i))
		}
	case reflect.Map:
		if a.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			t.Fatalf("%s: clone shares map with source", path)
		}
		for _, key := range a.MapKeys() {
			assertNoAliasing(t, fmt.Sprintf("%s[%v]", path, key), a.MapIndex(key), b.MapIndex(key))
		}
	case reflect.Interface:
		if a.IsNil() {
			return
		}
		assertNoAliasing(t, path, a.Elem(), b.Elem())
	}
}

func TestGraphCloneIsDeep(t *testing.T) {
	var source Graph
	fillValue(reflect.ValueOf(&source).Elem(), 0)
	clone := source.Clone()
	if !reflect.DeepEqual(&source, clone) {
		t.Fatal("clone differs from source")
	}
	assertNoAliasing(t, "Graph", reflect.ValueOf(&source), reflect.ValueOf(clone))

	clone.Nodes[0].Title = "changed"
	clone.Nodes[0].Tags[0] = "changed"
	clone.UI.Positions["k0"] = Position{X: -1}
	clone.Flags["k0"].(map[string]any)["nested"].([]any)[0] = "changed"
	if source.Nodes[0].Title == "changed" || source.Nodes[0].Tags[0] == "changed" ||
		source.UI.Positions["k0"].X == -1 ||
		source.Flags["k0"].(map[string]any)["nested"].([]any)[0] == "changed" {
		t.Fatal("a write through the clone reached the source")
	}
}

func TestGraphCloneOfNilAndEmpty(t *testing.T) {
	var nilGraph *Graph
	if nilGraph.Clone() != nil {
		t.Fatal("nil graph should clone to nil")
	}
	empty := &Graph{Version: 1}
	clone := empty.Clone()
	if clone == empty || !reflect.DeepEqual(empty, clone) {
		t.Fatal("empty graph clone should be an equal, distinct value")
	}
	if clone.Nodes != nil || clone.UI != nil {
		t.Fatal("clone should keep nil fields nil (JSON and YAML output depend on it)")
	}
}

func BenchmarkGraphClone(b *testing.B) {
	g := &Graph{Version: 1, UI: &UIState{Positions: map[string]Position{}}}
	for i := range 2000 {
		id := fmt.Sprintf("n%05d", i)
		g.Nodes = append(g.Nodes, &Node{ID: id, Title: "Node " + id, Tags: []string{"a", "b"}})
		g.UI.Positions[id] = Position{X: float64(i)}
		if i > 0 {
			g.Edges = append(g.Edges, &Edge{From: fmt.Sprintf("n%05d", i-1), To: id})
		}
	}
	for b.Loop() {
		_ = g.Clone()
	}
}
