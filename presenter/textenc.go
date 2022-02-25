//-----------------------------------------------------------------------------
// Copyright (c) 2022 Detlef Stern
//
// This file is part of zettelstore slides application.
//
// Zettelstore slides application is licensed under the latest version of the
// EUPL (European Union Public License). Please see file LICENSE.txt for your
// rights and obligations under this license.
//-----------------------------------------------------------------------------

package main

import (
	"bytes"
	"io"
	"log"

	"zettelstore.de/c/zjson"
)

// func textNew(w io.Writer) *textV { return &textV{w: w} }
func textEncodeInline(a zjson.Array) string {
	var buf bytes.Buffer
	zjson.WalkInline(&textV{w: &buf}, a, 0)
	return buf.String()
}

type textV struct {
	w io.Writer
}

func (v *textV) WriteString(s string) { io.WriteString(v.w, s) }

func (v *textV) BlockArray(a zjson.Array, pos int) zjson.CloseFunc  { return nil }
func (v *textV) InlineArray(a zjson.Array, pos int) zjson.CloseFunc { return nil }
func (v *textV) ItemArray(a zjson.Array, pos int) zjson.CloseFunc {
	if pos > 0 {
		v.WriteString(" ")
	}
	return nil
}

func (v *textV) BlockObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	// log.Println(t, pos)
	if pos > 0 {
		v.WriteString(" ")
	}
	return true, nil
}

func (v *textV) InlineObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	switch t {
	case zjson.TypeText, zjson.TypeTag:
		v.WriteString(zjson.GetString(obj, zjson.NameString))
	case zjson.TypeSpace, zjson.TypeBreakSoft, zjson.TypeBreakHard:
		v.WriteString(" ")
	default:
		return true, nil
	}
	return false, nil
}

func (v *textV) Unexpected(val zjson.Value, pos int, exp string) {
	log.Printf("?%v %d %T %v\n", exp, pos, val, val)
}
