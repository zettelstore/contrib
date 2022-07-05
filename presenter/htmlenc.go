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
	"encoding/base64"
	"fmt"
	"io"
	"log"

	"codeberg.org/t73fde/sxpf"
	"zettelstore.de/c/api"
	"zettelstore.de/c/html"
	"zettelstore.de/c/zjson"
)

func htmlNew(w io.Writer, s *slideSet, ren renderer, headingOffset int, embedImage, extZettelLinks bool) *htmlV {
	env := html.NewEncEnvironment(w, headingOffset)
	enc := html.NewEncoder(w, headingOffset)
	v := &htmlV{
		env:            env,
		enc:            enc,
		s:              s,
		ren:            ren,
		embedImage:     embedImage,
		extZettelLinks: extZettelLinks,
	}

	enc.ChangeTypeFunc(zjson.TypeBlock, v.makeVisitBlock)
	enc.SetTypeFunc(zjson.TypeVerbatimEval, v.makeVisitVerbatimEval(enc.MustGetTypeFunc(zjson.TypeVerbatimCode)))
	enc.SetTypeFunc(zjson.TypeVerbatimComment, html.DoNothingTypeFunc)
	enc.SetTypeFunc(zjson.TypeLink, v.visitLink)
	enc.SetTypeFunc(zjson.TypeEmbed, v.visitEmbed)
	enc.SetTypeFunc(zjson.TypeLiteralComment, html.DoNothingTypeFunc)
	return v
}

func (v *htmlV) SetUnique(s string)            { v.enc.SetUnique(s) }
func (v *htmlV) SetCurrentSlide(si *slideInfo) { v.curSlide = si }

func zEncodeInline(baseV *htmlV, in zjson.Array) string {
	if baseV == nil {
		return html.EncodeInline(nil, in, false, false)
	}
	return html.EncodeInline(baseV.enc, in, true, true)
}
func (v *htmlV) TraverseBlock(bn zjson.Array) { v.enc.TraverseBlock(bn) }

func evaluateInline(baseV *htmlV, in sxpf.Sequence) string {
	if baseV == nil {
		return html.EvaluateInline(nil, in, false, false)
	}
	return html.EvaluateInline(baseV.env, in, true, true)
}

type htmlV struct {
	env            *html.EncEnvironment
	enc            *html.Encoder
	s              *slideSet
	curSlide       *slideInfo
	ren            renderer
	embedImage     bool
	extZettelLinks bool
}

// embedImage, extZettelLinks
// false, true for presentation
// true, false for handout
// false, false for manual (?)

func (v *htmlV) Write(b []byte) (int, error)       { return v.enc.Write(b) }
func (v *htmlV) WriteString(s string) (int, error) { return v.enc.WriteString(s) }

func (v *htmlV) WriteEndnotes() { v.enc.WriteEndnotes() }

func (v *htmlV) makeVisitBlock(oldF html.TypeFunc) html.TypeFunc {
	return func(enc *html.Encoder, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
		a := zjson.GetAttributes(obj)
		if val, found := a.Get(""); found {
			switch val {
			case "show":
				if ren := v.ren; ren == nil || ren.Role() != SlideRoleShow {
					return false, nil
				}
				enc.WriteString("<aside class=\"notes\">\n")
				return true, func() { enc.WriteString("\n</aside>") }
			case "handout":
				if ren := v.ren; ren == nil || ren.Role() != SlideRoleHandout {
					return false, nil
				}
				enc.WriteString("<aside class=\"handout\">\n")
				return true, func() { enc.WriteString("\n</aside>") }
			case "both":
				ren := v.ren
				if ren == nil {
					return false, nil
				}
				switch ren.Role() {
				case SlideRoleShow:
					enc.WriteString("<aside class=\"notes\">\n")
				case SlideRoleHandout:
					enc.WriteString("<aside class=\"handout\">\n")
				default:
					return false, nil
				}
				return true, func() { enc.WriteString("\n</aside>") }
			}
		}
		return oldF(enc, obj, pos)
	}
}

func (*htmlV) makeVisitVerbatimEval(visitVerbatimCode html.TypeFunc) html.TypeFunc {
	return func(enc *html.Encoder, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
		a := zjson.GetAttributes(obj)
		if syntax, found := a.Get(""); found && syntax == SyntaxMermaid {
			enc.WriteString("<div class=\"mermaid\">\n")
			enc.WriteString(zjson.GetString(obj, zjson.NameString))
			enc.WriteString("</div>")
			return false, nil
		}
		return visitVerbatimCode(enc, obj, pos)
	}
}

func (v *htmlV) visitLink(enc *html.Encoder, obj zjson.Object, _ int) (bool, zjson.CloseFunc) {
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
		zid := api.ZettelID(ref)
		// TODO: check for fragment
		if si := v.curSlide.FindSlide(zid); si != nil {
			// TODO: process and add fragment
			a = a.Set("href", fmt.Sprintf("#(%d)", si.Number))
		} else if v.extZettelLinks {
			// TODO: make link absolute
			a = a.Set("href", "/"+ref)
			suffix = "&#10547;"
		}
	case zjson.RefStateBased, zjson.RefStateHosted:
		a = a.Set("href", ref)
	case zjson.RefStateSelf:
		// TODO: check for current slide to avoid self reference collisions
		a = a.Set("href", ref)
	case zjson.RefStateBroken:
		a = a.AddClass("broken")
	default:
		log.Println("LINK", q, ref)
	}

	if len(a) > 0 {
		v.WriteString("<a")
		v.enc.WriteAttributes(a)
		v.Write([]byte{'>'})
	}

	children := true
	if len(in) == 0 {
		v.WriteString(ref)
		children = false
	}
	return children, func() {
		v.WriteString("</a>")
		v.WriteString(suffix)
	}
}

func (v *htmlV) visitEmbed(enc *html.Encoder, obj zjson.Object, _ int) (bool, zjson.CloseFunc) {
	src := zjson.GetString(obj, zjson.NameString)
	if syntax := zjson.GetString(obj, zjson.NameString2); syntax == api.ValueSyntaxSVG {
		v.visitEmbedSVG(src)
		return false, nil
	}
	zid := api.ZettelID(src)
	if v.s != nil && v.embedImage && zid.IsValid() && v.s.HasImage(zid) {
		if img, found := v.s.GetImage(zid); found {
			var buf bytes.Buffer
			buf.WriteString("data:image/")
			buf.WriteString(img.syntax)
			buf.WriteString(";base64,")
			base64.NewEncoder(base64.StdEncoding, &buf).Write(img.data)
			enc.WriteImage(obj, buf.String())
			return false, nil
		}
	}
	if zid.IsValid() {
		src = "/" + src + ".content"
	}
	enc.WriteImage(obj, src)
	return false, nil
}
func (v *htmlV) visitEmbedSVG(src string) {
	zid := api.ZettelID(src)
	if v.s != nil && zid.IsValid() && v.s.HasImage(zid) {
		if svg, found := v.s.GetImage(zid); found && svg.syntax == api.ValueSyntaxSVG {
			v.Write(svg.data)
			return
		}
	}
	fmt.Fprintf(v, "<figure><embed type=\"image/svg+xml\" src=\"%s\" /></figure>\n", "/"+src+".svg")
}
