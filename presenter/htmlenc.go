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
	"strings"

	"codeberg.org/t73fde/sxpf"
	"zettelstore.de/c/api"
	"zettelstore.de/c/html"
	"zettelstore.de/c/sexpr"
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
		hasMermaid:     false,
	}

	enc.ChangeTypeFunc(zjson.TypeBlock, v.makeVisitBlock)
	enc.SetTypeFunc(zjson.TypeVerbatimEval, v.makeVisitVerbatimEval(enc.MustGetTypeFunc(zjson.TypeVerbatimCode)))
	enc.SetTypeFunc(zjson.TypeVerbatimComment, html.DoNothingTypeFunc)
	enc.SetTypeFunc(zjson.TypeLink, v.visitLink)
	enc.SetTypeFunc(zjson.TypeEmbed, v.visitEmbed)
	enc.SetTypeFunc(zjson.TypeLiteralComment, html.DoNothingTypeFunc)

	env.Builtins.Set(sexpr.SymLinkZettel, sxpf.NewBuiltin("linkZ", true, 2, -1, v.generateLinkZettel))
	env.Builtins.Set(sexpr.SymLinkExternal, sxpf.NewBuiltin("linkE", true, 2, -1, v.generateLinkExternal))
	env.Builtins.Set(sexpr.SymEmbed, sxpf.NewBuiltin("embed", true, 3, -1, v.generateEmbed))

	env.Builtins.Set(sexpr.SymVerbatimEval, v.makeEvaluateVerbatimEval(env.Builtins.MustLookupForm(sexpr.SymVerbatimEval)))
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

func evaluateInline(baseV *htmlV, in *sxpf.Pair) string {
	if baseV == nil {
		return html.EvaluateInline(nil, in, false, false)
	}
	return html.EvaluateInline(baseV.env, in, true, true)
}
func (v *htmlV) EvaluateBlock(bn *sxpf.Pair) { v.env.EvalPair(bn) }

type htmlV struct {
	env            *html.EncEnvironment
	enc            *html.Encoder
	s              *slideSet
	curSlide       *slideInfo
	ren            renderer
	embedImage     bool
	extZettelLinks bool
	hasMermaid     bool
}

// embedImage, extZettelLinks
// false, true for presentation
// true, false for handout
// false, false for manual (?)

func (v *htmlV) HasMermaid() bool { return v.hasMermaid }

func (v *htmlV) Write(b []byte) (int, error)       { return v.enc.Write(b) }
func (v *htmlV) WriteString(s string) (int, error) { return v.enc.WriteString(s) }

func (v *htmlV) ZWriteEndnotes() { v.enc.WriteEndnotes() }
func (v *htmlV) WriteEndnotes()  { v.env.WriteEndnotes() }

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

func (v *htmlV) makeVisitVerbatimEval(visitVerbatimCode html.TypeFunc) html.TypeFunc {
	return func(enc *html.Encoder, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
		a := zjson.GetAttributes(obj)
		if syntax, found := a.Get(""); found && syntax == SyntaxMermaid {
			v.hasMermaid = true
			enc.WriteString("<div class=\"mermaid\">\n")
			enc.WriteString(zjson.GetString(obj, zjson.NameString))
			enc.WriteString("</div>")
			return false, nil
		}
		return visitVerbatimCode(enc, obj, pos)
	}
}

func (v *htmlV) makeEvaluateVerbatimEval(oldForm sxpf.Form) sxpf.Form {
	return sxpf.NewBuiltin(
		"verb-eval", true, 1, -1,
		func(env sxpf.Environment, args *sxpf.Pair, _ int) (sxpf.Value, error) {
			if hasMermaidAttribute(args) {
				v.hasMermaid = true
				v.WriteString("<div class=\"mermaid\">\n")
				v.WriteString(v.env.GetString(args.GetTail()))
				v.WriteString("</div>")
				return nil, nil
			}
			return oldForm.Call(env, args)
		})
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
func (v *htmlV) generateLinkZettel(senv sxpf.Environment, args *sxpf.Pair, _ int) (sxpf.Value, error) {
	env := senv.(*html.EncEnvironment)
	if a, refValue, ok := html.PrepareLink(env, args); ok {
		zid, _, _ := strings.Cut(refValue, "#")
		// TODO: check for fragment
		if si := v.curSlide.FindSlide(api.ZettelID(zid)); si != nil {
			// TODO: process and add fragment
			a = a.Set("href", fmt.Sprintf("#(%d)", si.Number))
			html.WriteLink(env, args, a, refValue, "")
		} else if v.extZettelLinks {
			// TODO: make link absolute
			a = a.Set("href", "/"+zid)
			html.WriteLink(env, args, a, refValue, "&#10547;")
		} else {
			html.WriteLink(env, args, a, refValue, "")
		}
	}
	return nil, nil
}

func (v *htmlV) generateLinkExternal(senv sxpf.Environment, args *sxpf.Pair, _ int) (sxpf.Value, error) {
	env := senv.(*html.EncEnvironment)
	if a, refValue, ok := html.PrepareLink(env, args); ok {
		a = a.Set("href", refValue).
			AddClass("external").
			Set("target", "_blank").
			Set("rel", "noopener noreferrer")
		html.WriteLink(env, args, a, refValue, "&#10138;")
	}
	return nil, nil
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
func (v *htmlV) generateEmbed(senv sxpf.Environment, args *sxpf.Pair, arity int) (sxpf.Value, error) {
	env := senv.(*html.EncEnvironment)
	ref := env.GetPair(args.GetTail())
	src := env.GetString(ref.GetTail())
	if syntax := env.GetString(args.GetTail().GetTail()); syntax == api.ValueSyntaxSVG {
		// TODO
		v.visitEmbedSVG(src)
		return nil, nil
	}
	zid := api.ZettelID(src)
	if v.s != nil && v.embedImage && zid.IsValid() && v.s.HasImage(zid) {
		if img, found := v.s.GetImage(zid); found {
			var buf bytes.Buffer
			buf.WriteString("data:image/")
			buf.WriteString(img.syntax)
			buf.WriteString(";base64,")
			base64.NewEncoder(base64.StdEncoding, &buf).Write(img.data)
			env.WriteImageWithSource(args, buf.String())
			return nil, nil
		}
	}
	if zid.IsValid() {
		src = "/" + src + ".content"
	}
	env.WriteImageWithSource(args, src)
	return nil, nil
}
