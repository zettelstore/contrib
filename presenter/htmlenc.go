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
	"strings"

	"codeberg.org/t73fde/sxpf"
	"zettelstore.de/c/api"
	"zettelstore.de/c/html"
	"zettelstore.de/c/sexpr"
)

func htmlNew(w io.Writer, s *slideSet, ren renderer, headingOffset int, embedImage, extZettelLinks bool) *htmlV {
	env := html.NewEncEnvironment(w, headingOffset)
	v := &htmlV{
		env:            env,
		s:              s,
		ren:            ren,
		embedImage:     embedImage,
		extZettelLinks: extZettelLinks,
		hasMermaid:     false,
	}

	env.Builtins.Set(sexpr.SymRegionBlock, v.makeEvaluateBlock(env.Builtins.MustLookupForm(sexpr.SymRegionBlock)))
	env.Builtins.Set(sexpr.SymVerbatimEval, v.makeEvaluateVerbatimEval(env.Builtins.MustLookupForm(sexpr.SymVerbatimEval)))
	env.Builtins.Set(sexpr.SymVerbatimComment, sxpf.NewBuiltin("verb-comm", true, 1, -1, formNothing))
	env.Builtins.Set(sexpr.SymLinkZettel, sxpf.NewBuiltin("linkZ", true, 2, -1, v.generateLinkZettel))
	env.Builtins.Set(sexpr.SymLinkExternal, sxpf.NewBuiltin("linkE", true, 2, -1, v.generateLinkExternal))
	env.Builtins.Set(sexpr.SymEmbed, sxpf.NewBuiltin("embed", true, 3, -1, v.generateEmbed))
	env.Builtins.Set(sexpr.SymLiteralComment, sxpf.NewBuiltin("lit-comm", true, 1, -1, formNothing))
	return v
}

func formNothing(sxpf.Environment, *sxpf.List, int) (sxpf.Object, error) { return nil, nil }

func (v *htmlV) SetUnique(s string)            { v.env.SetUnique(s) }
func (v *htmlV) SetCurrentSlide(si *slideInfo) { v.curSlide = si }

func evaluateInline(baseV *htmlV, in *sxpf.List) string {
	if baseV == nil {
		return html.EvaluateInline(nil, in, false, false)
	}
	return html.EvaluateInline(baseV.env, in, true, true)
}
func (v *htmlV) EvaluateBlock(bn *sxpf.List) { v.env.EvalPair(bn) }

type htmlV struct {
	env            *html.EncEnvironment
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

func (v *htmlV) Write(b []byte) (int, error)       { return v.env.Write(b) }
func (v *htmlV) WriteString(s string) (int, error) { return v.env.WriteString(s) }

func (v *htmlV) WriteEndnotes() { v.env.WriteEndnotes() }

func (v *htmlV) makeEvaluateBlock(oldForm sxpf.Form) sxpf.Form {
	return sxpf.NewBuiltin(
		"block", true, 2, -1,
		func(env sxpf.Environment, args *sxpf.List, _ int) (sxpf.Object, error) {
			a := sexpr.GetAttributes(v.env.GetPair(args))
			if val, found := a.Get(""); found {
				switch val {
				case "show":
					if ren := v.ren; ren == nil || ren.Role() != SlideRoleShow {
						return nil, nil
					}
					v.WriteString("<aside class=\"notes\">")
					v.EvaluateBlock(v.env.GetPair(args.Tail()))
					v.WriteString("</aside>")
					return nil, nil
				case "handout":
					if ren := v.ren; ren == nil || ren.Role() != SlideRoleHandout {
						return nil, nil
					}
					v.WriteString("<aside class=\"handout\">")
					v.EvaluateBlock(v.env.GetPair(args.Tail()))
					v.WriteString("</aside>")
					return nil, nil
				case "both":
					ren := v.ren
					if ren == nil {
						return nil, nil
					}
					switch ren.Role() {
					case SlideRoleShow:
						v.WriteString("<aside class=\"notes\">")
					case SlideRoleHandout:
						v.WriteString("<aside class=\"handout\">")
					default:
						return nil, nil
					}
					v.EvaluateBlock(v.env.GetPair(args.Tail()))
					v.WriteString("</aside>")
					return nil, nil
				}
			}
			return oldForm.Call(env, args)
		})
}

func (v *htmlV) makeEvaluateVerbatimEval(oldForm sxpf.Form) sxpf.Form {
	return sxpf.NewBuiltin(
		"verb-eval", true, 1, -1,
		func(env sxpf.Environment, args *sxpf.List, _ int) (sxpf.Object, error) {
			if hasMermaidAttribute(args) {
				v.hasMermaid = true
				v.WriteString("<div class=\"mermaid\">\n")
				v.WriteString(v.env.GetString(args.Tail()))
				v.WriteString("</div>")
				return nil, nil
			}
			return oldForm.Call(env, args)
		})
}

func (v *htmlV) generateLinkZettel(senv sxpf.Environment, args *sxpf.List, _ int) (sxpf.Object, error) {
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

func (v *htmlV) generateLinkExternal(senv sxpf.Environment, args *sxpf.List, _ int) (sxpf.Object, error) {
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
func (v *htmlV) generateEmbed(senv sxpf.Environment, args *sxpf.List, arity int) (sxpf.Object, error) {
	env := senv.(*html.EncEnvironment)
	ref := env.GetPair(args.Tail())
	src := env.GetString(ref.GetTail())
	if syntax := env.GetString(args.Tail().Tail()); syntax == api.ValueSyntaxSVG {
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
