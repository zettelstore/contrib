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
	"fmt"
	"io"
	"log"
	"strconv"

	"zettelstore.de/c/api"
	"zettelstore.de/c/html"
	"zettelstore.de/c/zjson"
)

func newHTML(w io.Writer, lang string, headingOffset int, unique string) *htmlV {
	return &htmlV{
		w:             w,
		headingOffset: headingOffset,
		unique:        unique,
	}
}

type footnodeInfo struct {
	note  zjson.Array
	attrs zjson.Attributes
}

type htmlV struct {
	w             io.Writer
	headingOffset int
	unique        string
	footnotes     []footnodeInfo
	visibleSpace  bool
}

func (v *htmlV) Write(b []byte) (int, error)       { return v.w.Write(b) }
func (v *htmlV) WriteString(s string) (int, error) { return io.WriteString(v.w, s) }
func (v *htmlV) WriteEOL() (int, error)            { return v.w.Write([]byte{'\n'}) }
func (v *htmlV) WriteEscaped(s string) (int, error) {
	if v.visibleSpace {
		return html.EscapeVisible(v, s)
	}
	return html.Escape(v, s)
}
func (v *htmlV) WriteAttribute(s string) { html.AttributeEscape(v, s) }

func (v *htmlV) visitEndnotes() {
	if len(v.footnotes) == 0 {
		return
	}
	v.WriteString("<ol class=\"zs-endnotes\">\n")
	for i, fni := range v.footnotes {
		n := i + 1
		fmt.Fprintf(v, `<li value="%d" id="fn:%s%d" class="footnote">`, n, v.unique, n)
		zjson.WalkInline(v, fni.note, 0)
		fmt.Fprintf(v, ` <a href="#fnref:%s%d">&#x21a9;&#xfe0e;</a></li>`, v.unique, n)
		v.WriteEOL()
	}
	v.footnotes = nil
	v.WriteString("</ol>\n")
}

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
	case zjson.TypeHeading:
		return v.visitHeading(obj)
	case zjson.TypeBreakThematic:
		v.WriteString("<hr>")
		return false, nil
	case zjson.TypeListBullet:
		return v.visitList(obj, "ul")
	case zjson.TypeListOrdered:
		return v.visitList(obj, "ol")
	case zjson.TypeDescription:
		return v.visitDescription(obj)
	case zjson.TypeTable:
		return v.visitTable(obj)
	case zjson.TypeExcerpt:
		return v.visitExcerpt(obj)
	case zjson.TypeVerbatimCode:
		return v.visitVerbatimCode(obj)
	case zjson.TypeBLOB:
		return v.visitBLOB(obj)
	case zjson.TypeText:
		v.WriteString(zjson.GetString(obj, zjson.NameString))
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
	case zjson.TypeEmbed:
		return v.visitEmbed(obj)
	case zjson.TypeFootnote:
		return v.visitFootnote(obj)
	case zjson.TypeFormatDelete:
		return v.visitFormat(obj, "del")
	case zjson.TypeFormatEmph:
		return v.visitFormat(obj, "em")
	case zjson.TypeFormatInsert:
		return v.visitFormat(obj, "ins")
	case zjson.TypeFormatMonospace:
		return v.visitFormat(obj, "tt")
	case zjson.TypeFormatQuote:
		return v.visitFormat(obj, "q")
	case zjson.TypeFormatSpan:
		return v.visitFormat(obj, "span")
	case zjson.TypeFormatStrong:
		return v.visitFormat(obj, "strong")
	case zjson.TypeFormatSub:
		return v.visitFormat(obj, "sub")
	case zjson.TypeFormatSuper:
		return v.visitFormat(obj, "sup")
	case zjson.TypeLiteralCode:
		return v.visitLiteral(obj, "code")
	case zjson.TypeLiteralInput:
		return v.visitLiteral(obj, "kbd")
	case zjson.TypeLiteralOutput:
		return v.visitLiteral(obj, "samp")
	}
	fmt.Fprintln(v, obj)
	log.Printf("%T %v\n", obj, obj)
	return true, nil
}

func (v *htmlV) NoValue(val zjson.Value, pos int)   { log.Printf("?NOV %d %T %v\n", pos, val, val) }
func (v *htmlV) NoArray(val zjson.Value, pos int)   { log.Println("?NOA", pos, val) }
func (v *htmlV) NoObject(obj zjson.Object, pos int) { log.Println("?NOO", pos, obj) }

func (v *htmlV) visitInline(val zjson.Value) {
	if a, ok := val.(zjson.Array); ok {
		for i, elem := range a {
			zjson.WalkObject(v, elem, i)
		}
	}
}

func (v *htmlV) visitHeading(obj zjson.Object) (bool, zjson.EndFunc) {
	level, err := strconv.Atoi(zjson.GetNumber(obj))
	if err != nil {
		return true, nil
	}
	level += v.headingOffset
	fmt.Fprintf(v, "<h%v>", level)
	return true, func() { fmt.Fprintf(v, "</h%v>", level) }
}

func (v *htmlV) visitList(obj zjson.Object, tag string) (bool, zjson.EndFunc) {
	fmt.Fprintf(v, "<%s>\n", tag)
	return true, func() {
		fmt.Fprintf(v, "</%s>\n", tag)
	}
}

func (v *htmlV) visitDescription(obj zjson.Object) (bool, zjson.EndFunc) {
	descrs := zjson.GetArray(obj, zjson.NameDescription)
	v.WriteString("<dl>\n")
	for _, elem := range descrs {
		descr := zjson.MakeArray(elem)
		if len(descr) == 0 {
			continue
		}
		v.WriteString("<dt>")
		v.visitInline(descr[0])
		v.WriteString("</dt>\n")
		if len(descr) == 1 {
			continue
		}
		for _, ddv := range descr[1:] {
			dd := zjson.MakeArray(ddv)
			if len(dd) == 0 {
				continue
			}
			v.WriteString("<dd>")
			zjson.WalkBlock(v, dd, 0)
			v.WriteString("</dd>\n")
		}
	}
	v.WriteString("</dl>")
	return false, nil
}
func (v *htmlV) visitTable(obj zjson.Object) (bool, zjson.EndFunc) {
	tdata := zjson.GetArray(obj, zjson.NameTable)
	if len(tdata) != 2 {
		return false, nil
	}
	hArray := zjson.MakeArray(tdata[0])
	bArray := zjson.MakeArray(tdata[1])
	v.WriteString("<table>\n")
	if len(hArray) > 0 {
		v.WriteString("<thead>\n")
		v.visitRow(hArray, "th")
		v.WriteString("</thead>\n")
	}
	if len(bArray) > 0 {
		v.WriteString("<tbody>\n")
		for _, row := range bArray {
			if rArray := zjson.MakeArray(row); rArray != nil {
				v.visitRow(rArray, "td")
			}
		}
		v.WriteString("</tbody>\n")
	}
	v.WriteString("</table>")
	return false, nil
}
func (v *htmlV) visitRow(row zjson.Array, tag string) {
	v.WriteString("<tr>")
	for _, cell := range row {
		if cArray := zjson.MakeArray(cell); len(cArray) == 2 {
			switch a := zjson.MakeString(cArray[0]); a {
			case zjson.AlignLeft:
				fmt.Fprintf(v, `<%s class="zs-left">`, tag)
			case zjson.AlignCenter:
				fmt.Fprintf(v, `<%s class="zs-center">`, tag)
			case zjson.AlignRight:
				fmt.Fprintf(v, `<%s class="zs-right">`, tag)
			default:
				fmt.Fprintf(v, "<%s>", tag)
			}
			v.visitInline(cArray[1])
			fmt.Fprintf(v, "</%s>", tag)
		}
	}
	v.WriteString("</tr>\n")
}

func (v *htmlV) visitExcerpt(obj zjson.Object) (bool, zjson.EndFunc) {
	v.WriteString("<blockquote>\n")
	if blocks := zjson.GetArray(obj, zjson.NameBlock); blocks != nil {
		zjson.WalkBlock(v, blocks, 0)
	}
	if cite := zjson.GetArray(obj, zjson.NameInline); cite != nil {
		v.WriteString("\n<cite>")
		zjson.WalkInline(v, cite, 0)
		v.WriteString("</cite>")
	}
	v.WriteString("\n</blockquote>")
	return false, nil
}

func (v *htmlV) visitVerbatimCode(obj zjson.Object) (bool, zjson.EndFunc) {
	oldVisible := v.visibleSpace
	a := zjson.GetAttributes(obj)
	if a.HasDefault() {
		v.visibleSpace = true
		a = a.Clone().RemoveDefault()
	}
	v.WriteString("<pre><code")
	v.visitAttributes(a)
	v.Write([]byte{'>'})
	v.WriteEscaped(zjson.GetString(obj, zjson.NameString))
	v.WriteString("</code></pre>")
	v.visibleSpace = oldVisible
	return false, nil
}

func (v *htmlV) visitBLOB(obj zjson.Object) (bool, zjson.EndFunc) {
	bType := zjson.GetString(obj, zjson.NameString)
	if bType == api.ValueSyntaxSVG {
		v.WriteString(zjson.GetString(obj, zjson.NameString3))
	} else if bType != "" {
		fmt.Fprintf(v, `<img src="data:image/%s;base64,`, bType)
		v.WriteString(zjson.GetString(obj, zjson.NameBinary))
		if title := zjson.GetString(obj, zjson.NameString2); title != "" {
			v.WriteString(`" title="`)
			v.WriteAttribute(title)
		}
		v.WriteString(`">`)
	}
	return false, nil
}

func (v *htmlV) visitLink(obj zjson.Object) (bool, zjson.EndFunc) {
	s := zjson.GetString(obj, zjson.NameString)
	a := zjson.GetAttributes(obj)
	a = a.Clone().Set("href", s)
	suffix := ""
	switch q := zjson.GetString(obj, zjson.NameString2); q {
	case zjson.RefStateExternal:
		a = a.AddClass("zs-external").
			Set("target", "_blank").
			Set("rel", "noopener noreferrer")
		suffix = "&#10138;"
	case zjson.RefStateZettel, zjson.RefStateBased, zjson.RefStateHosted:
	case zjson.RefStateBroken:
		a = a.AddClass("zs-broken")
	default:
		log.Println("LINK", q, s)
	}
	v.WriteString("<a")
	v.visitAttributes(a)
	v.Write([]byte{'>'})

	children := true
	if zjson.GetArray(obj, zjson.NameInline) == nil {
		v.WriteString(s)
		children = false
	}
	return children, func() {
		v.WriteString("</a>")
		v.WriteString(suffix)
	}
}
func (v *htmlV) visitEmbed(obj zjson.Object) (bool, zjson.EndFunc) {
	// TODO: Move embed text into title attribute --> textenc
	src := zjson.GetString(obj, zjson.NameString)
	if zid := api.ZettelID(src); zid.IsValid() {
		src = "/z/" + src
	}
	fmt.Fprintf(v, `<img src="%s">`, src)
	return false, nil
}

func (v *htmlV) visitFootnote(obj zjson.Object) (bool, zjson.EndFunc) {
	if fn := zjson.GetArray(obj, zjson.NameInline); fn != nil {
		v.footnotes = append(v.footnotes, footnodeInfo{fn, zjson.GetAttributes(obj)})
		n := len(v.footnotes)
		fmt.Fprintf(v,
			`<sup id="fnref:%s%d"><a href="#fn:%s%d">%d</a></sup>`,
			v.unique, n, v.unique, n, n)
	}
	return false, nil
}

func (v *htmlV) visitFormat(obj zjson.Object, tag string) (bool, zjson.EndFunc) {
	v.Write([]byte{'<'})
	v.WriteString(tag)
	v.visitAttributes(zjson.GetAttributes(obj))
	v.Write([]byte{'>'})
	return true, func() { fmt.Fprintf(v, "</%s>", tag) }
}

func (v *htmlV) visitLiteral(obj zjson.Object, tag string) (bool, zjson.EndFunc) {
	oldVisible := v.visibleSpace
	a := zjson.GetAttributes(obj)
	if a.HasDefault() {
		v.visibleSpace = true
		a = a.Clone().RemoveDefault()
	}
	v.Write([]byte{'<'})
	v.WriteString(tag)
	v.visitAttributes(a)
	v.Write([]byte{'>'})
	v.WriteEscaped(zjson.GetString(obj, zjson.NameString))
	v.WriteString("</")
	v.WriteString(tag)
	v.Write([]byte{'>'})
	v.visibleSpace = oldVisible
	return false, nil
}

func (v *htmlV) visitAttributes(a zjson.Attributes) {
	if len(a) == 0 {
		return
	}
	for _, key := range a.Keys() {
		val, found := a.Get(key)
		if !found {
			continue
		}
		v.Write([]byte{' '})
		v.WriteString(key)
		v.WriteString(`="`)
		v.WriteAttribute(val)
		v.Write([]byte{'"'})
	}
}

// Everything below this line should move into client/zjson
