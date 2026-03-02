package enzyme

import (
	"strings"
	"testing"
)

func TestGoDigestSimple(t *testing.T) {
	src := `package main

import "fmt"

type Foo struct {
	Name string
}

func (f Foo) Hello() string {
	return fmt.Sprintf("hello %s", f.Name)
}

func main() {
	f := Foo{Name: "world"}
	fmt.Println(f.Hello())
}
`
	ge := GoSource{}
	ch := ge.Digest(strings.NewReader(src))

	types := make(map[string]int)
	var elems []string
	for e := range ch {
		types[e.Type()]++
		if e.Value() != nil && e.Value() != "" {
			elems = append(elems, e.Type()+":"+e.Value().(string))
		}
	}

	t.Logf("element types: %v", types)
	t.Logf("named elements: %v", elems)

	if types["package"] != 1 {
		t.Error("expected exactly 1 package element")
	}
	if types["func"] < 1 {
		t.Error("expected at least 1 func element")
	}
	if types["method"] < 1 {
		t.Error("expected at least 1 method element")
	}
	if types["type"] < 1 {
		t.Error("expected at least 1 type element")
	}
	if types["import"] < 1 {
		t.Error("expected at least 1 import element")
	}
}

func TestGoCanDigest(t *testing.T) {
	goCode := []byte("package main\n\nfunc main() {}\n")
	notGo := []byte("hello world this is not go")

	e := GoSource{}
	if !e.CanDigest(goCode) {
		t.Error("should detect Go source")
	}
	if e.CanDigest(notGo) {
		t.Error("should reject non-Go text")
	}
}
