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
	"fmt"
	"io"
	"log"
	"strconv"

	"zettelstore.de/c/api"
	"zettelstore.de/c/html"
	"zettelstore.de/c/zjson"
)

func htmlNew(w io.Writer, headingOffset int, unique string) *htmlV {
	return &htmlV{
		w:             w,
		headingOffset: headingOffset,
		unique:        unique,
	}
}

func htmlEncodeInline(in zjson.Array) string {
	var buf bytes.Buffer
	zjson.WalkInline(&htmlV{w: &buf}, in, 0)
	return buf.String()
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
	v.WriteString("<ol class=\"zp-endnotes\">\n")
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

func (v *htmlV) BlockArray(a zjson.Array, pos int) zjson.CloseFunc  { return nil }
func (v *htmlV) InlineArray(a zjson.Array, pos int) zjson.CloseFunc { return nil }
func (v *htmlV) ItemArray(a zjson.Array, pos int) zjson.CloseFunc {
	v.WriteString("<li>")
	return func() { v.WriteString("</li>\n") }
}

func (v *htmlV) BlockObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	if pos > 0 {
		v.WriteEOL()
	}
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
	case zjson.TypeListQuotation:
		return v.visitQuotation(obj)
	case zjson.TypeTable:
		return v.visitTable(obj)
	case zjson.TypeBlock, zjson.TypePoem:
		return v.visitRegion(obj, "div")
	case zjson.TypeExcerpt:
		return v.visitRegion(obj, "blockquote")
	case zjson.TypeVerbatimCode:
		return v.visitVerbatimCode(obj)
	case zjson.TypeBLOB:
		return v.visitBLOB(obj)
	}
	fmt.Fprintln(v, obj)
	log.Printf("B%T %v\n", obj, obj)
	return true, nil
}

func (v *htmlV) Unexpected(val zjson.Value, pos int, exp string) {
	log.Printf("?%v %d %T %v\n", exp, pos, val, val)
}

func (v *htmlV) visitInline(val zjson.Value) {
	if a, ok := val.(zjson.Array); ok {
		for i, elem := range a {
			zjson.WalkInlineObject(v, elem, i)
		}
	}
}

func (v *htmlV) visitHeading(obj zjson.Object) (bool, zjson.CloseFunc) {
	level, err := strconv.Atoi(zjson.GetNumber(obj))
	if err != nil {
		return true, nil
	}
	level += v.headingOffset
	fmt.Fprintf(v, "<h%v>", level)
	return true, func() { fmt.Fprintf(v, "</h%v>", level) }
}

func (v *htmlV) visitList(obj zjson.Object, tag string) (bool, zjson.CloseFunc) {
	fmt.Fprintf(v, "<%s>\n", tag)
	return true, func() {
		fmt.Fprintf(v, "</%s>\n", tag)
	}
}

func (v *htmlV) visitDescription(obj zjson.Object) (bool, zjson.CloseFunc) {
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

func (v *htmlV) visitQuotation(obj zjson.Object) (bool, zjson.CloseFunc) {
	v.WriteString("<blockquote>")
	inPara := false
	for i, item := range zjson.GetArray(obj, zjson.NameList) {
		bl, ok := item.(zjson.Array)
		if !ok {
			v.Unexpected(item, i, "Quotation array")
			continue
		}
		if p := getParagraph(bl); p != nil {
			if inPara {
				v.WriteEOL()
			} else {
				v.WriteString("<p>")
				inPara = true
			}
			zjson.WalkInline(v, p, 0)
		} else {
			if inPara {
				v.WriteString("</p>")
				inPara = false
			}
			zjson.WalkBlock(v, bl, 0)
		}
	}
	if inPara {
		v.WriteString("</p>")
	}
	v.WriteString("</blockquote>")
	return false, nil
}
func getParagraph(a zjson.Array) zjson.Array {
	if len(a) != 1 {
		return nil
	}
	if o := zjson.MakeObject(a[0]); o != nil {
		if zjson.GetString(o, zjson.NameType) == zjson.TypeParagraph {
			return zjson.GetArray(o, zjson.NameInline)
		}
	}
	return nil
}

func (v *htmlV) visitTable(obj zjson.Object) (bool, zjson.CloseFunc) {
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
				fmt.Fprintf(v, `<%s class="zp-left">`, tag)
			case zjson.AlignCenter:
				fmt.Fprintf(v, `<%s class="zp-center">`, tag)
			case zjson.AlignRight:
				fmt.Fprintf(v, `<%s class="zp-right">`, tag)
			default:
				fmt.Fprintf(v, "<%s>", tag)
			}
			v.visitInline(cArray[1])
			fmt.Fprintf(v, "</%s>", tag)
		}
	}
	v.WriteString("</tr>\n")
}
func (v *htmlV) visitRegion(obj zjson.Object, tag string) (bool, zjson.CloseFunc) {
	v.Write([]byte{'<'})
	v.WriteString(tag)
	v.visitAttributes(zjson.GetAttributes(obj))
	v.WriteString(">\n")
	if blocks := zjson.GetArray(obj, zjson.NameBlock); blocks != nil {
		zjson.WalkBlock(v, blocks, 0)
	}
	if cite := zjson.GetArray(obj, zjson.NameInline); cite != nil {
		v.WriteString("\n<cite>")
		zjson.WalkInline(v, cite, 0)
		v.WriteString("</cite>")
	}
	v.WriteString("\n</")
	v.WriteString(tag)
	v.Write([]byte{'>'})
	return false, nil
}

func (v *htmlV) visitVerbatimCode(obj zjson.Object) (bool, zjson.CloseFunc) {
	saveVisible := v.visibleSpace
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
	v.visibleSpace = saveVisible
	return false, nil
}

func (v *htmlV) visitBLOB(obj zjson.Object) (bool, zjson.CloseFunc) {
	switch s := zjson.GetString(obj, zjson.NameString); s {
	case "":
	case api.ValueSyntaxSVG:
		v.writeSVG(obj)
	default:
		v.writeDataImage(obj, s, zjson.GetString(obj, zjson.NameString2))
	}
	return false, nil
}
func (v *htmlV) writeSVG(obj zjson.Object) {
	if svg := zjson.GetString(obj, zjson.NameString3); svg != "" {
		// TODO: add inline text / title as description
		v.WriteString(svg)
	}
}
func (v *htmlV) writeDataImage(obj zjson.Object, syntax, title string) {
	if b := zjson.GetString(obj, zjson.NameBinary); b != "" {
		v.WriteString(`<img src="data:image/`)
		v.WriteString(syntax)
		v.WriteString(";base64,")
		v.WriteString(b)
		if title != "" {
			v.WriteString(`" title="`)
			v.WriteAttribute(title)
		}
		v.WriteString(`">`)
	}
}

func (v *htmlV) InlineObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	switch t {
	case zjson.TypeText:
		v.WriteString(zjson.GetString(obj, zjson.NameString))
		return false, nil
	case zjson.TypeSpace:
		return v.visitSpace(obj)
	case zjson.TypeBreakSoft:
		v.WriteEOL()
		return false, nil
	case zjson.TypeBreakHard:
		v.WriteString("<br>")
		return false, nil
	case zjson.TypeTag:
		return v.visitTag(obj)
	case zjson.TypeLink:
		return v.visitLink(obj)
	case zjson.TypeEmbed:
		return v.visitEmbed(obj)
	case zjson.TypeEmbedBLOB:
		return v.visitEmbedBLOB(obj)
	case zjson.TypeCitation:
		return v.visitCite(obj)
	case zjson.TypeMark:
		return v.visitMark(obj)
	case zjson.TypeFootnote:
		return v.visitFootnote(obj)
	case zjson.TypeFormatDelete:
		return v.visitFormat(obj, "del")
	case zjson.TypeFormatEmph:
		return v.visitFormat(obj, "em")
	case zjson.TypeFormatInsert:
		return v.visitFormat(obj, "ins")
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
	case zjson.TypeLiteralComment:
		return v.visitLiteralComment(obj)
	case zjson.TypeLiteralInput:
		return v.visitLiteral(obj, "kbd")
	case zjson.TypeLiteralOutput:
		return v.visitLiteral(obj, "samp")
	case zjson.TypeLiteralHTML, zjson.TypeVerbatimHTML:
		return v.visitHTML(obj)
	}
	fmt.Fprintln(v, obj)
	log.Printf("I%T %v\n", obj, obj)
	return true, nil
}

func (v *htmlV) visitSpace(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
		v.WriteString(s)
	} else {
		v.Write([]byte{' '})
	}
	return false, nil
}

func (v *htmlV) visitTag(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
		v.Write([]byte{'#'})
		v.WriteString(s)
	}
	return false, nil
}

func (v *htmlV) visitLink(obj zjson.Object) (bool, zjson.CloseFunc) {
	s := zjson.GetString(obj, zjson.NameString)
	a := zjson.GetAttributes(obj)
	a = a.Clone().Set("href", s)
	suffix := ""
	switch q := zjson.GetString(obj, zjson.NameString2); q {
	case zjson.RefStateExternal:
		a = a.AddClass("zp-external").
			Set("target", "_blank").
			Set("rel", "noopener noreferrer")
		suffix = "&#10138;"
	case zjson.RefStateZettel, zjson.RefStateBased, zjson.RefStateHosted:
	case zjson.RefStateSelf:
		// TODO: check for current slide to avoid self reference collisions
	case zjson.RefStateBroken:
		a = a.AddClass("zp-broken")
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

func (v *htmlV) visitEmbed(obj zjson.Object) (bool, zjson.CloseFunc) {
	src := zjson.GetString(obj, zjson.NameString)
	if zid := api.ZettelID(src); zid.IsValid() {
		src = "/z/" + src
	}
	v.WriteString(`<img src="`)
	v.WriteString(src)
	if title := zjson.GetArray(obj, zjson.NameInline); len(title) > 0 {
		s := textEncodeInline(title)
		v.WriteString(`" title="`)
		v.WriteEscaped(s)
	}
	v.WriteString(`">`)
	return false, nil
}

func (v *htmlV) visitEmbedBLOB(obj zjson.Object) (bool, zjson.CloseFunc) {
	switch s := zjson.GetString(obj, zjson.NameString); s {
	case "":
	case api.ValueSyntaxSVG:
		v.writeSVG(obj)
	default:
		v.writeDataImage(obj, s, textEncodeInline(zjson.GetArray(obj, zjson.NameInline)))
	}
	return false, nil
}

func (v *htmlV) visitCite(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
		v.WriteString(s)
		if zjson.GetArray(obj, zjson.NameInline) != nil {
			v.WriteString(", ")
		}
	}
	return true, nil
}

func (v *htmlV) visitMark(obj zjson.Object) (bool, zjson.CloseFunc) {
	if q := zjson.GetString(obj, zjson.NameString2); q != "" {
		v.WriteString(`<a id="`)
		if v.unique != "" {
			v.WriteString(v.unique)
			v.Write([]byte{':'})
		}
		v.WriteString(q)
		v.WriteString(`">`)
		return true, func() {
			v.WriteString("</a>")
		}
	}
	return true, nil
}

func (v *htmlV) visitFootnote(obj zjson.Object) (bool, zjson.CloseFunc) {
	if fn := zjson.GetArray(obj, zjson.NameInline); fn != nil {
		v.footnotes = append(v.footnotes, footnodeInfo{fn, zjson.GetAttributes(obj)})
		n := len(v.footnotes)
		fmt.Fprintf(v,
			`<sup id="fnref:%s%d"><a href="#fn:%s%d">%d</a></sup>`,
			v.unique, n, v.unique, n, n)
	}
	return false, nil
}

func (v *htmlV) visitFormat(obj zjson.Object, tag string) (bool, zjson.CloseFunc) {
	v.Write([]byte{'<'})
	v.WriteString(tag)
	v.visitAttributes(zjson.GetAttributes(obj))
	v.Write([]byte{'>'})
	return true, func() { fmt.Fprintf(v, "</%s>", tag) }
}

func (v *htmlV) visitLiteral(obj zjson.Object, tag string) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
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
		v.WriteEscaped(s)
		v.WriteString("</")
		v.WriteString(tag)
		v.Write([]byte{'>'})
		v.visibleSpace = oldVisible
	}
	return false, nil
}

func (v *htmlV) visitLiteralComment(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
		v.WriteString("<!-- ")
		v.WriteString(s)
		v.WriteString(" -->")
	}
	return false, nil
}

func (v *htmlV) visitHTML(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" && html.IsSave(s) {
		v.WriteString(s)
	}
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
