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
	"zettelstore.de/c/text"
	"zettelstore.de/c/zjson"
)

type TypeFunc func(obj zjson.Object) (bool, zjson.CloseFunc)
type typeMap map[string]TypeFunc

type Encoder struct {
	tm            typeMap
	w             io.Writer
	headingOffset int
	unique        string
	footnotes     []footnodeInfo
	writeFootnote bool
	writeComment  bool
	visibleSpace  bool
}
type footnodeInfo struct {
	note  zjson.Array
	attrs zjson.Attributes
}

func NewEncoder(w io.Writer, headingOffset int, writeComment bool) *Encoder {
	enc := &Encoder{
		w:             w,
		headingOffset: headingOffset,
		unique:        "",
		footnotes:     nil,
		writeFootnote: false,
		writeComment:  writeComment,
		visibleSpace:  false,
	}
	enc.setupTypeMap()
	return enc
}
func (enc *Encoder) setupTypeMap() {
	enc.tm = typeMap{
		// Block
		zjson.TypeParagraph: func(zjson.Object) (bool, zjson.CloseFunc) {
			enc.WriteString("<p>")
			return true, func() { enc.WriteString("</p>") }
		},
		zjson.TypeHeading:         enc.visitHeading,
		zjson.TypeBreakThematic:   func(zjson.Object) (bool, zjson.CloseFunc) { enc.WriteString("<hr>"); return false, nil },
		zjson.TypeListBullet:      func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitList(obj, "ul") },
		zjson.TypeListOrdered:     func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitList(obj, "ol") },
		zjson.TypeDescrList:       enc.visitDescription,
		zjson.TypeListQuotation:   enc.visitQuotation,
		zjson.TypeTable:           enc.visitTable,
		zjson.TypeBlock:           enc.visitBlock,
		zjson.TypePoem:            func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitRegion(obj, "div") },
		zjson.TypeExcerpt:         func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitRegion(obj, "blockquote") },
		zjson.TypeVerbatimCode:    enc.visitVerbatimCode,
		zjson.TypeVerbatimEval:    enc.visitVerbatimCode,
		zjson.TypeVerbatimComment: enc.visitVerbatimComment,
		zjson.TypeVerbatimHTML:    enc.visitHTML,
		zjson.TypeBLOB:            enc.visitBLOB,

		// Inline
		zjson.TypeText: func(obj zjson.Object) (bool, zjson.CloseFunc) {
			enc.WriteString(zjson.GetString(obj, zjson.NameString))
			return false, nil
		},
		zjson.TypeSpace: enc.visitSpace,
		zjson.TypeBreakSoft: func(zjson.Object) (bool, zjson.CloseFunc) {
			enc.WriteEOL()
			return false, nil
		},
		zjson.TypeBreakHard: func(zjson.Object) (bool, zjson.CloseFunc) {
			enc.WriteString("<br>")
			return false, nil
		},
		zjson.TypeTag:            enc.visitTag,
		zjson.TypeLink:           enc.visitLink,
		zjson.TypeEmbed:          enc.visitEmbed,
		zjson.TypeEmbedBLOB:      enc.visitEmbedBLOB,
		zjson.TypeCitation:       enc.visitCite,
		zjson.TypeMark:           enc.visitMark,
		zjson.TypeFootnote:       enc.visitFootnote,
		zjson.TypeFormatDelete:   func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitFormat(obj, "del") },
		zjson.TypeFormatEmph:     func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitFormat(obj, "em") },
		zjson.TypeFormatInsert:   func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitFormat(obj, "ins") },
		zjson.TypeFormatQuote:    func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitFormat(obj, "q") },
		zjson.TypeFormatSpan:     func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitFormat(obj, "span") },
		zjson.TypeFormatStrong:   func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitFormat(obj, "strong") },
		zjson.TypeFormatSub:      func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitFormat(obj, "sub") },
		zjson.TypeFormatSuper:    func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitFormat(obj, "sup") },
		zjson.TypeLiteralCode:    enc.visitCode,
		zjson.TypeLiteralComment: enc.visitLiteralComment,
		zjson.TypeLiteralInput:   func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitLiteral(obj, "kbd") },
		zjson.TypeLiteralOutput:  func(obj zjson.Object) (bool, zjson.CloseFunc) { return enc.visitLiteral(obj, "samp") },
		zjson.TypeLiteralHTML:    enc.visitHTML,
	}
}

func (enc *Encoder) SetTypeFunc(t string, f TypeFunc) { enc.tm[t] = f }
func (enc *Encoder) GetTypeFunc(t string) (TypeFunc, bool) {
	tf, found := enc.tm[t]
	return tf, found
}
func (enc *Encoder) MustGetTypeFunc(t string) TypeFunc {
	tf, found := enc.tm[t]
	if !found {
		panic(t)
	}
	return tf
}

func (enc *Encoder) SetUnique(s string)            { enc.unique = s }
func (enc *Encoder) TraverseBlock(bn zjson.Array)  { zjson.WalkBlock(enc, bn, 0) }
func (enc *Encoder) TraverseInline(in zjson.Array) { zjson.WalkInline(enc, in, 0) }
func (enc *Encoder) TraverseInlineObjects(val zjson.Value) {
	if a, ok := val.(zjson.Array); ok {
		for i, elem := range a {
			zjson.WalkInlineObject(enc, elem, i)
		}
	}
}
func EncodeInline(baseEnc *Encoder, in zjson.Array) string {
	var buf bytes.Buffer
	enc := Encoder{w: &buf}
	enc.setupTypeMap()
	if baseEnc != nil {
		enc.writeFootnote = baseEnc.writeFootnote
		enc.footnotes = baseEnc.footnotes
	}
	zjson.WalkInline(&enc, in, 0)
	if baseEnc != nil {
		baseEnc.footnotes = enc.footnotes
	}
	return buf.String()
}

func (enc *Encoder) WriteEndnotes() {
	if len(enc.footnotes) == 0 {
		return
	}
	enc.WriteString("<ol class=\"endnotes\">\n")
	for i, fni := range enc.footnotes {
		n := i + 1
		fmt.Fprintf(enc, `<li value="%d" id="fn:%s%d" class="footnote">`, n, enc.unique, n)
		zjson.WalkInline(enc, fni.note, 0)
		fmt.Fprintf(enc, ` <a href="#fnref:%s%d">&#x21a9;&#xfe0e;</a></li>`, enc.unique, n)
		enc.WriteEOL()
	}
	enc.footnotes = nil
	enc.WriteString("</ol>\n")
}

func (enc *Encoder) Write(b []byte) (int, error)        { return enc.w.Write(b) }
func (enc *Encoder) WriteString(s string) (int, error)  { return io.WriteString(enc.w, s) }
func (enc *Encoder) WriteEOL() (int, error)             { return enc.w.Write([]byte{'\n'}) }
func (enc *Encoder) WriteEscaped(s string) (int, error) { return html.Escape(enc, s) }
func (enc *Encoder) WriteEscapedLiteral(s string) (int, error) {
	if enc.visibleSpace {
		return html.EscapeVisible(enc, s)
	}
	return html.EscapeLiteral(enc, s)
}
func (enc *Encoder) WriteAttribute(s string) { html.AttributeEscape(enc, s) }

func (enc *Encoder) BlockArray(a zjson.Array, pos int) zjson.CloseFunc  { return nil }
func (enc *Encoder) InlineArray(a zjson.Array, pos int) zjson.CloseFunc { return nil }
func (enc *Encoder) ItemArray(a zjson.Array, pos int) zjson.CloseFunc {
	enc.WriteString("<li>")
	return func() { enc.WriteString("</li>\n") }
}
func (enc *Encoder) Unexpected(val zjson.Value, pos int, exp string) {
	log.Printf("?%v %d %T %v\n", exp, pos, val, val)
}

func (enc *Encoder) BlockObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	if pos > 0 {
		enc.WriteEOL()
	}
	if fun, found := enc.tm[t]; found {
		return fun(obj)
	}
	fmt.Fprintln(enc, obj)
	log.Printf("B%T %v\n", obj, obj)
	return true, nil
}

func (enc *Encoder) visitHeading(obj zjson.Object) (bool, zjson.CloseFunc) {
	level, err := strconv.Atoi(zjson.GetNumber(obj))
	if err != nil {
		return true, nil
	}
	level += enc.headingOffset
	fmt.Fprintf(enc, "<h%v>", level)
	return true, func() { fmt.Fprintf(enc, "</h%v>", level) }
}

func (enc *Encoder) visitList(obj zjson.Object, tag string) (bool, zjson.CloseFunc) {
	fmt.Fprintf(enc, "<%s>\n", tag)
	return true, func() {
		fmt.Fprintf(enc, "</%s>", tag)
	}
}

func (enc *Encoder) visitDescription(obj zjson.Object) (bool, zjson.CloseFunc) {
	descrs := zjson.GetArray(obj, zjson.NameDescrList)
	enc.WriteString("<dl>\n")
	for _, elem := range descrs {
		dObj := zjson.MakeObject(elem)
		if dObj == nil {
			continue
		}
		enc.WriteString("<dt>")
		enc.TraverseInlineObjects(zjson.GetArray(dObj, zjson.NameInline))
		enc.WriteString("</dt>\n")
		descr := zjson.GetArray(dObj, zjson.NameDescription)
		if len(descr) == 0 {
			continue
		}
		for _, ddv := range descr {
			dd := zjson.MakeArray(ddv)
			if len(dd) == 0 {
				continue
			}
			enc.WriteString("<dd>")
			zjson.WalkBlock(enc, dd, 0)
			enc.WriteString("</dd>\n")
		}
	}
	enc.WriteString("</dl>")
	return false, nil
}

func (enc *Encoder) visitQuotation(obj zjson.Object) (bool, zjson.CloseFunc) {
	enc.WriteString("<blockquote>")
	inPara := false
	for i, item := range zjson.GetArray(obj, zjson.NameList) {
		bl, ok := item.(zjson.Array)
		if !ok {
			enc.Unexpected(item, i, "Quotation array")
			continue
		}
		if p := getParagraph(bl); p != nil {
			if inPara {
				enc.WriteEOL()
			} else {
				enc.WriteString("<p>")
				inPara = true
			}
			zjson.WalkInline(enc, p, 0)
		} else {
			if inPara {
				enc.WriteString("</p>")
				inPara = false
			}
			zjson.WalkBlock(enc, bl, 0)
		}
	}
	if inPara {
		enc.WriteString("</p>")
	}
	enc.WriteString("</blockquote>")
	return false, nil
}

// TODO: --> zjson
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

func (enc *Encoder) visitTable(obj zjson.Object) (bool, zjson.CloseFunc) {
	tdata := zjson.GetArray(obj, zjson.NameTable)
	if len(tdata) != 2 {
		return false, nil
	}
	hArray := zjson.MakeArray(tdata[0])
	bArray := zjson.MakeArray(tdata[1])
	enc.WriteString("<table>\n")
	if len(hArray) > 0 {
		enc.WriteString("<thead>\n")
		enc.visitRow(hArray, "th")
		enc.WriteString("</thead>\n")
	}
	if len(bArray) > 0 {
		enc.WriteString("<tbody>\n")
		for _, row := range bArray {
			if rArray := zjson.MakeArray(row); rArray != nil {
				enc.visitRow(rArray, "td")
			}
		}
		enc.WriteString("</tbody>\n")
	}
	enc.WriteString("</table>")
	return false, nil
}
func (enc *Encoder) visitRow(row zjson.Array, tag string) {
	enc.WriteString("<tr>")
	for _, cell := range row {
		if cObj := zjson.MakeObject(cell); cObj != nil {
			switch a := zjson.GetString(cObj, zjson.NameString); a {
			case zjson.AlignLeft:
				fmt.Fprintf(enc, `<%s class="left">`, tag)
			case zjson.AlignCenter:
				fmt.Fprintf(enc, `<%s class="center">`, tag)
			case zjson.AlignRight:
				fmt.Fprintf(enc, `<%s class="right">`, tag)
			default:
				fmt.Fprintf(enc, "<%s>", tag)
			}
			enc.TraverseInlineObjects(zjson.GetArray(cObj, zjson.NameInline))
			fmt.Fprintf(enc, "</%s>", tag)
		}
	}
	enc.WriteString("</tr>\n")
}
func (enc *Encoder) visitBlock(obj zjson.Object) (bool, zjson.CloseFunc) {
	a := zjson.GetAttributes(obj)
	if val, found := a.Get(""); found {
		zjson.SetAttributes(obj, a.Remove("").AddClass(val))
	}
	return enc.visitRegion(obj, "div")
}

func (enc *Encoder) visitRegion(obj zjson.Object, tag string) (bool, zjson.CloseFunc) {
	enc.Write([]byte{'<'})
	enc.WriteString(tag)
	enc.WriteAttributes(zjson.GetAttributes(obj))
	enc.WriteString(">\n")
	if blocks := zjson.GetArray(obj, zjson.NameBlock); blocks != nil {
		zjson.WalkBlock(enc, blocks, 0)
	}
	if cite := zjson.GetArray(obj, zjson.NameInline); cite != nil {
		enc.WriteString("\n<cite>")
		zjson.WalkInline(enc, cite, 0)
		enc.WriteString("</cite>")
	}
	enc.WriteString("\n</")
	enc.WriteString(tag)
	enc.Write([]byte{'>'})
	return false, nil
}

func (enc *Encoder) visitVerbatimCode(obj zjson.Object) (bool, zjson.CloseFunc) {
	s := zjson.GetString(obj, zjson.NameString)
	a := zjson.GetAttributes(obj)
	saveVisible := enc.visibleSpace
	if a.HasDefault() {
		enc.visibleSpace = true
		a = a.RemoveDefault()
	}
	enc.WriteString("<pre><code")
	enc.WriteAttributes(enc.setProgLang(a))
	enc.Write([]byte{'>'})
	enc.WriteEscapedLiteral(s)
	enc.WriteString("</code></pre>")
	enc.visibleSpace = saveVisible
	return false, nil
}

func (*Encoder) setProgLang(a zjson.Attributes) zjson.Attributes {
	if val, found := a.Get(""); found {
		a = a.AddClass("language-" + val).Remove("")
	}
	return a
}

func (enc *Encoder) visitVerbatimComment(obj zjson.Object) (bool, zjson.CloseFunc) {
	if enc.writeComment {
		if s := zjson.GetString(obj, zjson.NameString); s != "" {
			enc.WriteString("<!--\n")
			enc.WriteString(s) // Escape "-->"
			enc.WriteString("\n-->")
		}
	}
	return false, nil
}

func (enc *Encoder) visitBLOB(obj zjson.Object) (bool, zjson.CloseFunc) {
	switch s := zjson.GetString(obj, zjson.NameString); s {
	case "":
	case api.ValueSyntaxSVG:
		enc.WriteSVG(obj)
	default:
		enc.WriteDataImage(obj, s, zjson.GetString(obj, zjson.NameString2))
	}
	return false, nil
}
func (enc *Encoder) WriteSVG(obj zjson.Object) {
	if svg := zjson.GetString(obj, zjson.NameString3); svg != "" {
		// TODO: add inline text / title as description
		enc.WriteString("<p>")
		enc.WriteString(svg)
		enc.WriteString("</p>")
	}
}
func (enc *Encoder) WriteDataImage(obj zjson.Object, syntax, title string) {
	if b := zjson.GetString(obj, zjson.NameBinary); b != "" {
		enc.WriteString(`<p><img src="data:image/`)
		enc.WriteString(syntax)
		enc.WriteString(";base64,")
		enc.WriteString(b)
		if title != "" {
			enc.WriteString(`" title="`)
			enc.WriteAttribute(title)
		}
		enc.WriteString(`"></p>`)
	}
}

func (enc *Encoder) InlineObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	if fun, found := enc.tm[t]; found {
		return fun(obj)
	}
	fmt.Fprintln(enc, obj)
	log.Printf("I%T %v\n", obj, obj)
	return true, nil
}

func (enc *Encoder) visitSpace(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
		enc.WriteString(s)
	} else {
		enc.Write([]byte{' '})
	}
	return false, nil
}

func (enc *Encoder) visitTag(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
		enc.Write([]byte{'#'})
		enc.WriteString(s)
	}
	return false, nil
}

func (enc *Encoder) visitLink(obj zjson.Object) (bool, zjson.CloseFunc) {
	ref := zjson.GetString(obj, zjson.NameString)
	in := zjson.GetArray(obj, zjson.NameInline)
	if ref == "" {
		return len(in) > 0, nil
	}
	a := zjson.GetAttributes(obj)
	suffix := ""
	switch q := zjson.GetString(obj, zjson.NameString2); q {
	case zjson.RefStateExternal:
		a = a.Set("href", ref).
			AddClass("external").
			Set("target", "_blank").
			Set("rel", "noopener noreferrer")
		suffix = "&#10138;"
	case zjson.RefStateZettel:
		a = a.Set("href", ref)
	case zjson.RefStateBased, zjson.RefStateHosted:
		a = a.Set("href", ref)
	case zjson.RefStateSelf:
		a = a.Set("href", ref)
	case zjson.RefStateBroken:
		a = a.AddClass("broken")
	default:
		log.Println("LINK", q, ref)
	}

	if len(a) > 0 {
		enc.WriteString("<a")
		enc.WriteAttributes(a)
		enc.Write([]byte{'>'})
	}

	children := true
	if len(in) == 0 {
		enc.WriteString(ref)
		children = false
	}
	return children, func() {
		enc.WriteString("</a>")
		enc.WriteString(suffix)
	}
}

func (enc *Encoder) visitEmbed(obj zjson.Object) (bool, zjson.CloseFunc) {
	src := zjson.GetString(obj, zjson.NameString)
	if syntax := zjson.GetString(obj, zjson.NameString2); syntax == api.ValueSyntaxSVG {
		enc.visitEmbedSVG(src)
		return false, nil
	}
	zid := api.ZettelID(src)
	if zid.IsValid() {
		src = "/" + src + ".content"
	}
	enc.WriteString(`<img src="`)
	enc.WriteString(src)
	enc.WriteImageTitle(obj)
	return false, nil
}
func (enc *Encoder) visitEmbedSVG(src string) {
	fmt.Fprintf(enc, "<figure><embed type=\"image/svg+xml\" src=\"%s\" /></figure>\n", "/"+src+".svg")
}
func (enc *Encoder) WriteImageTitle(obj zjson.Object) {
	if title := zjson.GetArray(obj, zjson.NameInline); len(title) > 0 {
		s := text.EncodeInlineString(title)
		enc.WriteString(`" title="`)
		enc.WriteEscaped(s)
	}
	enc.Write([]byte{'"'})
	enc.WriteAttributes(zjson.GetAttributes(obj))
	enc.Write([]byte{'>'})
}

func (enc *Encoder) visitEmbedBLOB(obj zjson.Object) (bool, zjson.CloseFunc) {
	switch s := zjson.GetString(obj, zjson.NameString); s {
	case "":
	case api.ValueSyntaxSVG:
		enc.WriteSVG(obj)
	default:
		enc.WriteDataImage(obj, s, text.EncodeInlineString(zjson.GetArray(obj, zjson.NameInline)))
	}
	return false, nil
}

func (enc *Encoder) visitCite(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
		enc.WriteString(s)
		if zjson.GetArray(obj, zjson.NameInline) != nil {
			enc.WriteString(", ")
		}
	}
	return true, nil
}

func (enc *Encoder) visitMark(obj zjson.Object) (bool, zjson.CloseFunc) {
	if q := zjson.GetString(obj, zjson.NameString2); q != "" {
		enc.WriteString(`<a id="`)
		if enc.unique != "" {
			enc.WriteString(enc.unique)
			enc.Write([]byte{':'})
		}
		enc.WriteString(q)
		enc.WriteString(`">`)
		return true, func() {
			enc.WriteString("</a>")
		}
	}
	return true, nil
}

func (enc *Encoder) visitFootnote(obj zjson.Object) (bool, zjson.CloseFunc) {
	if enc.writeFootnote {
		if fn := zjson.GetArray(obj, zjson.NameInline); fn != nil {
			enc.footnotes = append(enc.footnotes, footnodeInfo{fn, zjson.GetAttributes(obj)})
			n := len(enc.footnotes)
			fmt.Fprintf(enc,
				`<sup id="fnref:%s%d"><a href="#fn:%s%d">%d</a></sup>`,
				enc.unique, n, enc.unique, n, n)
		}
	}
	return false, nil
}

func (enc *Encoder) visitFormat(obj zjson.Object, tag string) (bool, zjson.CloseFunc) {
	enc.Write([]byte{'<'})
	enc.WriteString(tag)
	a := zjson.GetAttributes(obj)
	if val, found := a.Get(""); found {
		a = a.Remove("").AddClass(val)
	}
	enc.WriteAttributes(a)
	enc.Write([]byte{'>'})
	return true, func() { fmt.Fprintf(enc, "</%s>", tag) }
}

func (enc *Encoder) visitCode(obj zjson.Object) (bool, zjson.CloseFunc) {
	zjson.SetAttributes(obj, enc.setProgLang(zjson.GetAttributes(obj)))
	return enc.visitLiteral(obj, "code")
}

func (enc *Encoder) visitLiteral(obj zjson.Object, tag string) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" {
		a := zjson.GetAttributes(obj)
		oldVisible := enc.visibleSpace
		if a.HasDefault() {
			enc.visibleSpace = true
			a = a.RemoveDefault()
		}
		enc.Write([]byte{'<'})
		enc.WriteString(tag)
		enc.WriteAttributes(a)
		enc.Write([]byte{'>'})
		enc.WriteEscapedLiteral(s)
		enc.WriteString("</")
		enc.WriteString(tag)
		enc.Write([]byte{'>'})
		enc.visibleSpace = oldVisible
	}
	return false, nil
}

func (enc *Encoder) visitLiteralComment(obj zjson.Object) (bool, zjson.CloseFunc) {
	if enc.writeComment {
		if s := zjson.GetString(obj, zjson.NameString); s != "" {
			enc.WriteString("<!-- ")
			enc.WriteString(s) // TODO: escape "-->"
			enc.WriteString(" -->")
		}
	}
	return false, nil
}

func (enc *Encoder) visitHTML(obj zjson.Object) (bool, zjson.CloseFunc) {
	if s := zjson.GetString(obj, zjson.NameString); s != "" && html.IsSave(s) {
		enc.WriteString(s)
	}
	return false, nil
}

func (enc *Encoder) WriteAttributes(a zjson.Attributes) {
	if len(a) == 0 {
		return
	}
	for _, key := range a.Keys() {
		if key == "" || key == "-" {
			continue
		}
		val, found := a.Get(key)
		if !found {
			continue
		}
		enc.Write([]byte{' '})
		enc.WriteString(key)
		enc.WriteString(`="`)
		enc.WriteAttribute(val)
		enc.Write([]byte{'"'})
	}
}
