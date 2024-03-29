//-----------------------------------------------------------------------------
// Copyright (c) 2022-present Detlef Stern
//
// This file is part of zettelstore slides application.
//
// Zettelstore slides application is licensed under the latest version of the
// EUPL (European Union Public License). Please see file LICENSE.txt for your
// rights and obligations under this license.
//-----------------------------------------------------------------------------

package main

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"

	"codeberg.org/t73fde/sxhtml"
	"codeberg.org/t73fde/sxpf"
	"codeberg.org/t73fde/sxpf/eval"
	"zettelstore.de/c/api"
	"zettelstore.de/c/shtml"
	"zettelstore.de/c/sz"
)

type htmlGenerator struct {
	tr         *shtml.Transformer
	s          *slideSet
	curSlide   *slideInfo
	hasMermaid bool
}

// embedImage, extZettelLinks
// false, true for presentation
// true, false for handout
// false, false for manual (?)

func newGenerator(sf sxpf.SymbolFactory, slides *slideSet, ren renderer, extZettelLinks, embedImage bool) *htmlGenerator {
	tr := shtml.NewTransformer(1, sf)
	gen := htmlGenerator{
		tr: tr,
		s:  slides,
	}
	tr.SetRebinder(func(te *shtml.TransformEnv) {
		te.Rebind(sz.NameSymRegionBlock, func(args []sxpf.Object, prevFn eval.Callable) sxpf.Object {
			attr, isCell := sxpf.GetCell(args[0])
			if !isCell {
				return nil
			}
			a := sz.GetAttributes(attr)
			if val, found := a.Get(""); found {
				switch val {
				case "show":
					if ren != nil {
						if ren.Role() == SlideRoleShow {
							classAttr := addClass(nil, "notes", sf)
							result := sxpf.MakeList(sf.MustMake("aside"), classAttr.Cons(sf.MustMake(sxhtml.NameSymAttr)))
							result.Tail().SetCdr(args[1])
							return result
						}
						return sxpf.Nil()
					}
				case "handout":
					if ren != nil {
						if ren.Role() == SlideRoleHandout {
							classAttr := addClass(nil, "handout", sf)
							result := sxpf.MakeList(sf.MustMake("aside"), classAttr.Cons(sf.MustMake(sxhtml.NameSymAttr)))
							result.Tail().SetCdr(args[1])
							return result
						}
						return sxpf.Nil()
					}
				case "both":
					if ren != nil {
						var classAttr *sxpf.Cell
						switch ren.Role() {
						case SlideRoleShow:
							classAttr = addClass(nil, "notes", sf)
						case SlideRoleHandout:
							classAttr = addClass(nil, "handout", sf)
						default:
							return sxpf.Nil()
						}
						result := sxpf.MakeList(sf.MustMake("aside"), classAttr.Cons(sf.MustMake(sxhtml.NameSymAttr)))
						result.Tail().SetCdr(args[1])
						return result
					}
				}
			}

			obj, err := prevFn.Call(nil, nil, args)
			if err != nil {
				return sxpf.Nil()
			}
			return obj
		})
		te.Rebind(sz.NameSymVerbatimEval, func(args []sxpf.Object, prevFn eval.Callable) sxpf.Object {
			attr, isCell := sxpf.GetCell(args[0])
			if !isCell {
				return nil
			}
			a := sz.GetAttributes(attr)
			if syntax, found := a.Get(""); found && syntax == SyntaxMermaid {
				gen.hasMermaid = true
				if mmCode, isString := sxpf.GetString(args[1]); isString {
					return sxpf.MakeList(
						sf.MustMake("div"),
						sxpf.MakeList(
							sf.MustMake(sxhtml.NameSymAttr),
							sxpf.Cons(sf.MustMake("class"), sxpf.MakeString("mermaid")),
						),
						mmCode,
					)
				}
			}
			obj, err := prevFn.Call(nil, nil, args)
			if err != nil {
				return sxpf.Nil()
			}
			return obj
		})
		te.Rebind(sz.NameSymVerbatimComment, func(_ []sxpf.Object, _ eval.Callable) sxpf.Object { return sxpf.Nil() })
		te.Rebind(sz.NameSymLinkZettel, func(args []sxpf.Object, prevFn eval.Callable) sxpf.Object {
			obj, err := prevFn.Call(nil, nil, args)
			if err != nil {
				return sxpf.Nil()
			}
			lst, isCell := sxpf.GetCell(obj)
			if !isCell {
				return obj
			}
			sym, isSymbol := sxpf.GetSymbol(lst.Car())
			if !isSymbol || !sym.IsEqual(sf.MustMake("a")) {
				return obj
			}
			attr, isCell := sxpf.GetCell(lst.Tail().Car())
			if !isCell {
				return obj
			}
			avals := attr.Tail()
			symHref := sf.MustMake("href")
			p := avals.Assoc(symHref)
			if p == nil {
				return obj
			}
			refVal, isString := sxpf.GetString(p.Cdr())
			if !isString {
				return obj
			}
			zid, _, _ := strings.Cut(refVal.String(), "#")
			if si := gen.curSlide.FindSlide(api.ZettelID(zid)); si != nil {
				avals = avals.Cons(sxpf.Cons(symHref, sxpf.MakeString(fmt.Sprintf("#(%d)", si.Number))))
			} else if extZettelLinks {
				// TODO: make link absolute
				avals = addClass(avals, "zettel", sf)
				attr.SetCdr(avals.Cons(sxpf.Cons(symHref, sxpf.MakeString("/"+zid))))
				return lst
			}
			attr.SetCdr(avals)
			return lst
		})
		te.Rebind(sz.NameSymLinkExternal, func(args []sxpf.Object, prevFn eval.Callable) sxpf.Object {
			obj, err := prevFn.Call(nil, nil, args)
			if err != nil {
				return sxpf.Nil()
			}
			lst, isCell := sxpf.GetCell(obj)
			if !isCell {
				return obj
			}
			attr, isCell := sxpf.GetCell(lst.Tail().Car())
			if !isCell {
				return obj
			}
			avals := attr.Tail()
			avals = addClass(avals, "external", sf)
			avals = avals.Cons(sxpf.Cons(sf.MustMake("target"), sxpf.MakeString("_blank")))
			avals = avals.Cons(sxpf.Cons(sf.MustMake("rel"), sxpf.MakeString("noopener noreferrer")))
			attr.SetCdr(avals)
			return lst
		})
		te.Rebind(sz.NameSymEmbed, func(args []sxpf.Object, prevFn eval.Callable) sxpf.Object {
			obj, err := prevFn.Call(nil, nil, args)
			if err != nil {
				return sxpf.Nil()
			}
			cell, isCell := sxpf.GetCell(obj)
			if !isCell {
				return obj
			}
			attr, isCell := sxpf.GetCell(cell.Tail().Car())
			if !isCell {
				return obj
			}
			avals := attr.Tail()
			symSrc := sf.MustMake("src")
			p := avals.Assoc(symSrc)
			if p == nil {
				return obj
			}
			zidVal, isString := sxpf.GetString(p.Cdr())
			if !isString {
				return obj
			}
			zid := api.ZettelID(zidVal)
			syntax, isString := sxpf.GetString(args[2])
			if !isString {
				return obj
			}
			if syntax == api.ValueSyntaxSVG {
				if gen.s != nil && zid.IsValid() && gen.s.HasImage(zid) {
					if svg, found := gen.s.GetImage(zid); found && svg.syntax == api.ValueSyntaxSVG {
						log.Println("SVGG", svg)
						return obj
					}
				}
				return sxpf.MakeList(
					sf.MustMake("figure"),
					sxpf.MakeList(
						sf.MustMake("embed"),
						sxpf.MakeList(
							sf.MustMake(sxhtml.NameSymAttr),
							sxpf.Cons(sf.MustMake("type"), sxpf.MakeString("image/svg+xml")),
							sxpf.Cons(symSrc, sxpf.MakeString("/"+string(zid)+".svg")),
						),
					),
				)
			}
			if !zid.IsValid() {
				return obj
			}
			var src string
			if gen.s != nil && embedImage && gen.s.HasImage(zid) {
				if img, found := gen.s.GetImage(zid); found {
					var sb strings.Builder
					sb.WriteString("data:image/")
					sb.WriteString(img.syntax)
					sb.WriteString(";base64,")
					base64.NewEncoder(base64.StdEncoding, &sb).Write(img.data)
					src = sb.String()
				}
			}
			if src == "" {
				src = "/" + string(zid) + ".content"
			}
			attr.SetCdr(avals.Cons(sxpf.Cons(symSrc, sxpf.MakeString(src))))
			return obj
		})
		te.Rebind(sz.NameSymLiteralComment, func([]sxpf.Object, eval.Callable) sxpf.Object { return sxpf.Nil() })
	})
	return &gen
}
func (gen *htmlGenerator) SetUnique(s string)            { gen.tr.SetUnique(s) }
func (gen *htmlGenerator) SetCurrentSlide(si *slideInfo) { gen.curSlide = si }

func (gen *htmlGenerator) Transform(astLst *sxpf.Cell) *sxpf.Cell {
	result, err := gen.tr.Transform(astLst)
	if err != nil {
		log.Println("ETRA", err)
	}
	return result
}

func (gen *htmlGenerator) Endnotes() *sxpf.Cell { return gen.tr.Endnotes() }

func (gen *htmlGenerator) writeHTMLDocument(w http.ResponseWriter, lang string, headHtml, bodyHtml *sxpf.Cell) {
	sf := gen.tr.SymbolFactory()
	var langAttr *sxpf.Cell
	if lang != "" {
		langAttr = sxpf.MakeList(sf.MustMake(sxhtml.NameSymAttr), sxpf.Cons(sf.MustMake("lang"), sxpf.MakeString(lang)))
	}
	if gen.hasMermaid {
		curr := bodyHtml.Tail().LastPair().AppendBang(sxpf.MakeList(
			sf.MustMake("script"),
			sxpf.MakeString("//"),
			sxpf.MakeList(sf.MustMake(sxhtml.NameSymCDATA), sxpf.MakeString(mermaid)),
		))
		curr.AppendBang(getJSScript("mermaid.initialize({startOnLoad:true});", sf))
	}
	zettelHtml := sxpf.MakeList(
		sf.MustMake(sxhtml.NameSymDoctype),
		sxpf.MakeList(sf.MustMake("html"), langAttr, headHtml, bodyHtml),
	)
	g := sxhtml.NewGenerator(sf, sxhtml.WithNewline)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	g.WriteHTML(w, zettelHtml)
}

func getJSScript(jsScript string, sf sxpf.SymbolFactory) *sxpf.Cell {
	return sxpf.MakeList(
		sf.MustMake("script"),
		sxpf.MakeList(sf.MustMake(sxhtml.NameSymNoEscape), sxpf.MakeString(jsScript)),
	)
}

func addClass(alist *sxpf.Cell, val string, sf sxpf.SymbolFactory) *sxpf.Cell {
	symClass := sf.MustMake("class")
	if p := alist.Assoc(symClass); p != nil {
		if s, ok := sxpf.GetString(p.Cdr()); ok {
			classVal := s.String()
			if strings.Contains(" "+classVal+" ", val) {
				return alist
			}
			return alist.Cons(sxpf.Cons(symClass, sxpf.MakeString(classVal+" "+val)))
		}
	}
	return alist.Cons(sxpf.Cons(symClass, sxpf.MakeString(val)))
}

//go:embed mermaid/mermaid.min.js
var mermaid string
