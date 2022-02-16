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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strconv"

	"zettelstore.de/c/zjson"
)

type zjsonObject = map[string]interface{}
type zjsonArray = []interface{}

func newHTML(w io.Writer, headingOffset int) *htmlV {
	return &htmlV{
		w:             w,
		headingOffset: headingOffset}
}

type htmlV struct {
	w             io.Writer
	headingOffset int
}

func (v *htmlV) Write(b []byte) (int, error)       { return v.w.Write(b) }
func (v *htmlV) WriteString(s string) (int, error) { return io.WriteString(v.w, s) }
func (v *htmlV) WriteEOL() (int, error)            { return v.w.Write([]byte{'\n'}) }

func (v *htmlV) Block(a zjson.Array, pos int) zjson.EndFunc {
	if pos > 0 {
		v.WriteEOL()
	}
	return nil
}
func (v *htmlV) Inline(a zjson.Array, pos int) zjson.EndFunc { return nil }
func (v *htmlV) Item(a zjson.Array, pos int) zjson.EndFunc {
	v.WriteString("<li>")
	return func() { v.WriteString("</li>\n") }
}

func (v *htmlV) Object(t string, obj zjson.Object, pos int) (bool, zjson.EndFunc) {
	switch t {
	case zjson.TypeParagraph:
		v.WriteString("<p>")
		return true, func() { v.WriteString("</p>") }
	case zjson.TypeListBullet:
		v.WriteString("<ul>\n")
		return true, func() { v.WriteString("</ul>\n") }
	case zjson.TypeListOrdered:
		v.WriteString("<ol>\n")
		return true, func() { v.WriteString("</ol>\n") }
	case zjson.TypeHeading:
		level, _ := strconv.Atoi(getNumber(obj))
		level += v.headingOffset
		fmt.Fprintf(v, "<h%v>", level)
		return true, func() { fmt.Fprintf(v, "</h%v>", level) }
	case zjson.TypeText:
		v.WriteString(getString(obj))
		return false, nil
	case zjson.TypeSpace:
		v.Write([]byte{' '})
		return false, nil
	case zjson.TypeBreakSoft:
		v.WriteEOL()
		return false, nil
	case zjson.TypeBreakHard:
		v.WriteString("<br>")
		return false, nil
	case zjson.TypeLink:
		return v.visitLink(obj)
	case zjson.TypeFormatEmph:
		return v.visitFormat(obj, "em")
	case zjson.TypeFormatMonospace:
		return v.visitFormat(obj, "tt")
	case zjson.TypeFormatStrong:
		return v.visitFormat(obj, "strong")
	case zjson.TypeLiteralCode:
		return v.visitLiteral(obj, "code")
	}
	fmt.Fprintln(v, obj)
	return true, nil
}

func (v *htmlV) visitLink(obj zjsonObject) (bool, zjson.EndFunc) {
	s := getString(obj)
	fmt.Fprintf(v, `<a href="%s">`, s)
	children := true
	if getInlines(obj) == nil {
		v.WriteString(s)
		children = false
	}
	return children, func() { v.WriteString("<a>") }
}
func (v *htmlV) visitFormat(obj zjsonObject, tag string) (bool, zjson.EndFunc) {
	fmt.Fprintf(v, "<%s>", tag)
	return true, func() { fmt.Fprintf(v, "</%s>", tag) }
}

func (v *htmlV) visitLiteral(obj zjsonObject, tag string) (bool, zjson.EndFunc) {
	fmt.Fprintf(v, "<%s>%s</%s>", tag, getString(obj), tag)
	return false, nil
}

func getInlines(obj zjsonObject) zjsonArray {
	a := obj[zjson.NameInline]
	if a == nil {
		return nil
	}
	return a.(zjsonArray)
}
func getNumber(obj zjsonObject) string  { return string(obj[zjson.NameNumeric].(json.Number)) }
func getString(obj zjsonObject) string  { return obj[zjson.NameString].(string) }
func getString2(obj zjsonObject) string { return obj[zjson.NameString2].(string) }

func (v *htmlV) NoValue(val zjson.Value, pos int)   { log.Println("?NOV", pos, val) }
func (v *htmlV) NoArray(val zjson.Value, pos int)   { log.Println("?NOA", pos, val) }
func (v *htmlV) NoObject(obj zjson.Object, pos int) { log.Println("?NOO", pos, obj) }
