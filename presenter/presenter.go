//-----------------------------------------------------------------------------
// Copyright (c) 2021-present Detlef Stern
//
// This file is part of zettelstore slides application.
//
// Zettelstore slides application is licensed under the latest version of the
// EUPL (European Union Public License). Please see file LICENSE.txt for your
// rights and obligations under this license.
//-----------------------------------------------------------------------------

// Package main is the starting point for the slides command.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"codeberg.org/t73fde/sxhtml"
	"codeberg.org/t73fde/sxpf"
	"golang.org/x/term"

	"zettelstore.de/c/api"
	"zettelstore.de/c/client"
	"zettelstore.de/c/sz"
	"zettelstore.de/c/text"
)

// Constants for minimum required version.
const (
	minMajor = 0
	minMinor = 11
)

func hasVersion(major, minor int) bool {
	if major < minMajor {
		return false
	}
	return minor >= minMinor
}

func main() {
	listenAddress := flag.String("l", ":23120", "Listen address")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		io.WriteString(out, "  [URL] URL of Zettelstore (default: \"http://127.0.0.1:23123\")\n")
	}
	flag.Parse()
	ctx := context.Background()
	c, err := getClient(ctx, flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to zettelstore: %v\n", err)
		os.Exit(2)
	}
	cfg, err := getConfig(ctx, c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to retrieve presenter config: %v\n", err)
		os.Exit(2)
	}

	http.HandleFunc("/", makeHandler(&cfg))
	http.Handle("/revealjs/", http.FileServer(http.FS(revealjs)))
	fmt.Println("Listening:", *listenAddress)
	http.ListenAndServe(*listenAddress, nil)
}

func getClient(ctx context.Context, base string) (*client.Client, error) {
	if base == "" {
		base = "http://127.0.0.1:23123"
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	withAuth, username, password := false, "", ""
	if uinfo := u.User; uinfo != nil {
		username = uinfo.Username()
		if pw, ok := uinfo.Password(); ok {
			password = pw
		}
		withAuth = true
		u.User = nil
	}
	c := client.NewClient(u)
	ver, err := c.GetVersionJSON(ctx)
	if err != nil {
		return nil, err
	}
	if ver.Major == -1 {
		fmt.Fprintln(os.Stderr, "Unknown zettelstore version. Use it at your own risk.")
	} else if !hasVersion(ver.Major, ver.Minor) {
		return nil, fmt.Errorf("need at least zettelstore version %d.%d but found only %d.%d", minMajor, minMinor, ver.Major, ver.Minor)
	}

	if !withAuth {
		err = c.ExecuteCommand(ctx, api.CommandAuthenticated)
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusUnauthorized {
			withAuth = true
		}
	}

	if withAuth {
		if username == "" {
			io.WriteString(os.Stderr, "Username: ")
			_, errUser := fmt.Fscanln(os.Stdin, &username)
			if errUser != nil {
				return nil, errUser
			}
		}
		if password == "" {
			io.WriteString(os.Stderr, "Password: ")
			pw, errPw := term.ReadPassword(int(os.Stdin.Fd()))
			io.WriteString(os.Stderr, "\n")
			if errPw != nil {
				return nil, errPw
			}
			password = string(pw)
		}
		c.SetAuth(username, password)
		errAuth := c.Authenticate(ctx)
		if errAuth != nil {
			return nil, errAuth
		}
	}

	return c, nil
}

const (
	zidConfig   = api.ZettelID("00009000001000")
	zidSlideCSS = api.ZettelID("00009000001005")
)

type slidesConfig struct {
	c            *client.Client
	astSF        sxpf.SymbolFactory
	zs           *sz.ZettelSymbols
	slideSetRole string
	author       string
}

func getConfig(ctx context.Context, c *client.Client) (slidesConfig, error) {
	m, err := c.GetMeta(ctx, zidConfig)
	if err != nil {
		return slidesConfig{}, err
	}
	astSF := sxpf.MakeMappedFactory()
	result := slidesConfig{
		c:            c,
		astSF:        astSF,
		zs:           &sz.ZettelSymbols{},
		slideSetRole: DefaultSlideSetRole,
	}
	result.zs.InitializeZettelSymbols(astSF)
	if ssr, ok := m[KeySlideSetRole]; ok {
		result.slideSetRole = ssr
	}
	if author, ok := m[KeyAuthor]; ok {
		result.author = author
	}
	return result, nil
}

func makeHandler(cfg *slidesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if zid, suffix := retrieveZidAndSuffix(path); zid != api.InvalidZID {
			switch suffix {
			case "reveal", "slide":
				processSlideSet(w, r, cfg, zid, &revealRenderer{cfg: cfg})
			case "html":
				processSlideSet(w, r, cfg, zid, &handoutRenderer{cfg: cfg})
			case "content":
				if content := retrieveContent(w, r, cfg.c, zid); len(content) > 0 {
					w.Write(content)
				}
			case "svg":
				if content := retrieveContent(w, r, cfg.c, zid); len(content) > 0 {
					io.WriteString(w, `<?xml version='1.0' encoding='utf-8'?>`)
					w.Write(content)
				}
			default:
				processZettel(w, r, cfg, zid)
			}
			return
		}
		if len(path) == 2 && ' ' < path[1] && path[1] <= 'z' {
			processList(w, r, cfg.c, cfg.astSF, cfg.zs)
			return
		}
		log.Println("NOTF", path)
		http.Error(w, fmt.Sprintf("Unhandled request %q", r.URL), http.StatusNotFound)
	}
}

func retrieveZidAndSuffix(path string) (api.ZettelID, string) {
	if path == "" {
		return api.InvalidZID, ""
	}
	if path == "/" {
		return api.ZidDefaultHome, ""
	}
	if path[0] == '/' {
		path = path[1:]
	}
	if len(path) < api.LengthZid {
		return api.InvalidZID, ""
	}
	zid := api.ZettelID(path[:api.LengthZid])
	if !zid.IsValid() {
		return api.InvalidZID, ""
	}
	if len(path) == api.LengthZid {
		return zid, ""
	}
	if path[api.LengthZid] != '.' {
		return api.InvalidZID, ""
	}
	if suffix := path[api.LengthZid+1:]; suffix != "" {
		return zid, suffix
	}
	return api.InvalidZID, ""
}

func retrieveContent(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID) []byte {
	content, err := c.GetZettel(r.Context(), zid, api.PartContent)
	if err != nil {
		reportRetrieveError(w, zid, err, "content")
		return nil
	}
	return content
}

func reportRetrieveError(w http.ResponseWriter, zid api.ZettelID, err error, objName string) {
	var cerr *client.Error
	if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
		http.Error(w, fmt.Sprintf("%s %s not found", objName, zid), http.StatusNotFound)
	} else {
		http.Error(w, fmt.Sprintf("Error retrieving %s %s: %s", zid, objName, err), http.StatusBadRequest)
	}
}

func processZettel(w http.ResponseWriter, r *http.Request, cfg *slidesConfig, zid api.ZettelID) {
	ctx := r.Context()
	sxZettel, err := cfg.c.GetEvaluatedSz(ctx, zid, api.PartZettel, cfg.astSF)
	if err != nil {
		reportRetrieveError(w, zid, err, "zettel")
		return
	}
	sxMeta, sxContent := sz.GetMetaContent(sxZettel)

	role := sxMeta.GetString(api.KeyRole)
	if role == cfg.slideSetRole {
		if slides := processSlideTOC(ctx, cfg.c, zid, sxMeta, cfg.zs, cfg.astSF); slides != nil {
			renderSlideTOC(w, slides, cfg.zs)
			return
		}
	}
	title := getSlideTitleZid(sxMeta, zid, cfg.zs)

	sf := sxpf.MakeMappedFactory()
	gen := newGenerator(sf, nil, nil, true, false)

	headHtml := getHTMLHead("", sf)
	headHtml.LastPair().AppendBang(sxpf.MakeList(sf.MustMake("title"), sxpf.MakeString(text.EvaluateInlineString(title))))

	headerHtml := sxpf.MakeList(
		sf.MustMake("header"),
		gen.Transform(title).Cons(sf.MustMake("h1")),
		getURLHtml(sxMeta, sf),
	)
	articleHtml := sxpf.MakeList(sf.MustMake("article"))
	curr := articleHtml
	for elem := gen.Transform(sxContent); elem != nil; elem = elem.Tail() {
		curr = curr.AppendBang(elem.Car())
	}
	footerHtml := sxpf.MakeList(
		sf.MustMake("footer"),
		gen.Endnotes(),
		sxpf.MakeList(
			sf.MustMake("p"),
			sxpf.MakeList(
				sf.MustMake("a"),
				sxpf.MakeList(
					sf.MustMake(sxhtml.NameSymAttr),
					sxpf.Cons(sf.MustMake("href"), sxpf.MakeString(cfg.c.Base()+"h/"+string(zid))),
				),
				sxpf.MakeString("\u266e"),
			),
		),
	)
	bodyHtml := sxpf.MakeList(sf.MustMake("body"), headerHtml, articleHtml, footerHtml)

	gen.writeHTMLDocument(w, sxMeta.GetString(api.KeyLang), headHtml, bodyHtml)
}

func getURLHtml(sxMeta sz.Meta, sf sxpf.SymbolFactory) *sxpf.List {
	var lst *sxpf.List
	for k, v := range sxMeta {
		if v.Type != api.MetaURL {
			continue
		}
		s, ok := v.Value.(sxpf.String)
		if !ok {
			continue
		}
		li := sxpf.MakeList(
			sf.MustMake("li"),
			sxpf.MakeString(k),
			sxpf.MakeString(": "),
			sxpf.MakeList(
				sf.MustMake("a"),
				sxpf.MakeList(
					sf.MustMake(sxhtml.NameSymAttr),
					sxpf.Cons(sf.MustMake("href"), s),
					sxpf.Cons(sf.MustMake("target"), sxpf.MakeString("_blank")),
				),
				s,
			),
			sxpf.MakeString("\u279a"),
		)
		lst = lst.Cons(li)
	}
	if lst != nil {
		return lst.Cons(sf.MustMake("ul"))
	}
	return nil
}

func processSlideTOC(ctx context.Context, c *client.Client, zid api.ZettelID, sxMeta sz.Meta, zs *sz.ZettelSymbols, astSF sxpf.SymbolFactory) *slideSet {
	o, err := c.GetZettelOrder(ctx, zid)
	if err != nil {
		return nil
	}
	slides := newSlideSetMeta(zid, sxMeta, zs)
	getZettel := func(zid api.ZettelID) ([]byte, error) { return c.GetZettel(ctx, zid, api.PartContent) }
	sGetZettel := func(zid api.ZettelID) (sxpf.Object, error) {
		return c.GetEvaluatedSz(ctx, zid, api.PartZettel, astSF)
	}
	setupSlideSet(slides, o.List, getZettel, sGetZettel, zs)
	return slides
}

func renderSlideTOC(w http.ResponseWriter, slides *slideSet, zs *sz.ZettelSymbols) {
	showTitle := slides.Title(zs)
	showSubtitle := slides.Subtitle()
	offset := 1
	if showTitle != nil {
		offset++
	}

	sf := sxpf.MakeMappedFactory()
	gen := newGenerator(sf, nil, nil, false, false)

	headHtml := getHTMLHead("", sf)
	headHtml.LastPair().AppendBang(sxpf.MakeList(sf.MustMake("title"), sxpf.MakeString(text.EvaluateInlineString(showTitle))))

	headerHtml := sxpf.MakeList(
		sf.MustMake("header"),
		gen.Transform(showTitle).Cons(sf.MustMake("h1")),
	)
	if showSubtitle != nil {
		headerHtml.LastPair().AppendBang(gen.Transform(showSubtitle).Cons(sf.MustMake("h2")))
	}
	lstSlide := sxpf.MakeList(sf.MustMake("ol"))
	curr := lstSlide
	curr = curr.AppendBang(sxpf.MakeList(sf.MustMake("li"), getSimpleLink("/"+string(slides.zid)+".slide#(1)", gen.Transform(showTitle), sf)))
	for si := slides.Slides(SlideRoleShow, offset); si != nil; si = si.Next() {
		slideTitle := gen.Transform(si.Slide.title)
		curr = curr.AppendBang(sxpf.MakeList(
			sf.MustMake("li"),
			getSimpleLink(fmt.Sprintf("/%s.slide#(%d)", slides.zid, si.Number), slideTitle, sf)))
	}
	bodyHtml := sxpf.MakeList(
		sf.MustMake("body"),
		headerHtml,
		lstSlide,
	)
	bodyHtml.LastPair().AppendBang(sxpf.MakeList(
		sf.MustMake("p"),
		getSimpleLink("/"+string(slides.zid)+".reveal", sxpf.MakeList(sxpf.MakeString("Reveal")), sf),
		sxpf.MakeString(", "),
		getSimpleLink("/"+string(slides.zid)+".html", sxpf.MakeList(sxpf.MakeString("Handout")), sf),
	))

	gen.writeHTMLDocument(w, slides.Lang(), headHtml, bodyHtml)
}

func processSlideSet(w http.ResponseWriter, r *http.Request, cfg *slidesConfig, zid api.ZettelID, ren renderer) {
	ctx := r.Context()
	o, err := cfg.c.GetZettelOrder(ctx, zid)
	if err != nil {
		reportRetrieveError(w, zid, err, "zettel")
		return
	}
	sMeta, err := cfg.c.GetEvaluatedSz(ctx, zid, api.PartMeta, cfg.astSF)
	if err != nil {
		http.Error(w, fmt.Sprintf("Unable to read zettel %s: %v", zid, err), http.StatusBadRequest)
		return
	}
	slides := newSlideSet(zid, sz.MakeMeta(sMeta), cfg.zs)
	getZettel := func(zid api.ZettelID) ([]byte, error) { return cfg.c.GetZettel(ctx, zid, api.PartContent) }
	sGetZettel := func(zid api.ZettelID) (sxpf.Object, error) {
		return cfg.c.GetEvaluatedSz(ctx, zid, api.PartZettel, cfg.astSF)
	}
	setupSlideSet(slides, o.List, getZettel, sGetZettel, cfg.zs)
	ren.Prepare(ctx)
	ren.Render(w, slides, slides.Author(cfg))
}

type renderer interface {
	Role() string
	Prepare(context.Context)
	Render(w http.ResponseWriter, slides *slideSet, author string)
}

type revealRenderer struct {
	cfg     *slidesConfig
	userCSS string
}

func (*revealRenderer) Role() string { return SlideRoleShow }
func (rr *revealRenderer) Prepare(ctx context.Context) {
	if data, err := rr.cfg.c.GetZettel(ctx, zidSlideCSS, api.PartContent); err == nil && len(data) > 0 {
		rr.userCSS = string(data)
	}
}
func (rr *revealRenderer) Render(w http.ResponseWriter, slides *slideSet, author string) {
	sf := sxpf.MakeMappedFactory()
	gen := newGenerator(sf, slides, rr, true, false)

	title := slides.Title(rr.cfg.zs)

	headHtml := getHTMLHead(rr.userCSS, sf)
	headHtml.LastPair().AppendBang(getHeadLink("stylesheet", "revealjs/reveal.css", sf)).
		AppendBang(getHeadLink("stylesheet", "revealjs/theme/white.css", sf)).
		AppendBang(getHeadLink("stylesheet", "revealjs/plugin/highlight/default.css", sf)).
		AppendBang(sxpf.MakeList(sf.MustMake("title"), sxpf.MakeString(text.EvaluateInlineString(title))))
	lang := slides.Lang()

	slidesHtml := sxpf.MakeList(sf.MustMake("div"), getClassAttr("slides", sf))
	revealHtml := sxpf.MakeList(sf.MustMake("div"), getClassAttr("reveal", sf), slidesHtml)
	offset := 1
	if title != nil {
		offset++
		hgroupHtml := sxpf.MakeList(
			sf.MustMake("hgroup"),
			gen.Transform(title).Cons(getClassAttr("title", sf)).Cons(sf.MustMake("h1")),
		)
		curr := hgroupHtml.LastPair()
		if subtitle := slides.Subtitle(); subtitle != nil {
			curr = curr.AppendBang(gen.Transform(subtitle).Cons(getClassAttr("subtitle", sf)).Cons(sf.MustMake("h2")))
		}
		if author != "" {
			curr.AppendBang(sxpf.MakeList(
				sf.MustMake("p"),
				getClassAttr("author", sf),
				sxpf.MakeString(author),
			))
		}
		slidesHtml = slidesHtml.LastPair().AppendBang(sxpf.MakeList(sf.MustMake("section"), hgroupHtml))
	}

	for si := slides.Slides(SlideRoleShow, offset); si != nil; si = si.Next() {
		gen.SetCurrentSlide(si)
		main := si.Child()
		rSlideHtml := getRevealSlide(gen, main, lang, sf)
		if sub := main.Next(); sub != nil {
			rSlideHtml = sxpf.MakeList(sf.MustMake("section"), rSlideHtml)
			curr := rSlideHtml.LastPair()
			for ; sub != nil; sub = sub.Next() {
				curr = curr.AppendBang(getRevealSlide(gen, sub, main.Slide.lang, sf))
			}
		}
		slidesHtml = slidesHtml.AppendBang(rSlideHtml)
	}

	bodyHtml := sxpf.MakeList(
		sf.MustMake("body"),
		revealHtml,
		getJSFileScript("revealjs/plugin/highlight/highlight.js", sf),
		getJSFileScript("revealjs/plugin/notes/notes.js", sf),
		getJSFileScript("revealjs/reveal.js", sf),
		getJSScript(`Reveal.initialize({width: 1920, height: 1024, center: true, slideNumber: "c", hash: true, plugins: [ RevealHighlight, RevealNotes ]});`, sf),
	)

	gen.writeHTMLDocument(w, lang, headHtml, bodyHtml)
}

func getRevealSlide(gen *htmlGenerator, si *slideInfo, lang string, sf sxpf.SymbolFactory) *sxpf.List {
	symAttr := sf.MustMake(sxhtml.NameSymAttr)
	attr := sxpf.MakeList(
		symAttr,
		sxpf.Cons(sf.MustMake("id"), sxpf.MakeString(fmt.Sprintf("(%d)", si.SlideNo))),
	)
	if slLang := si.Slide.lang; slLang != "" && slLang != lang {
		attr.LastPair().AppendBang(sxpf.Cons(sf.MustMake("lang"), sxpf.MakeString(slLang)))
	}

	var titleHtml *sxpf.List
	if title := si.Slide.title; title != nil {
		titleHtml = gen.Transform(title).Cons(sf.MustMake("h1"))
	}
	gen.SetUnique(fmt.Sprintf("%d:", si.Number))
	slideHtml := sxpf.MakeList(sf.MustMake("section"), attr, titleHtml)
	curr := slideHtml.LastPair()
	for content := si.Slide.content; content != nil; content = content.Tail() {
		curr = curr.AppendBang(gen.Transform(content.Head()))
	}
	curr.AppendBang(gen.Endnotes()).
		AppendBang(sxpf.MakeList(
			sf.MustMake("p"),
			sxpf.MakeList(
				sf.MustMake("a"),
				sxpf.MakeList(
					symAttr,
					sxpf.Cons(sf.MustMake("href"), sxpf.MakeString(string(si.Slide.zid))),
					sxpf.Cons(sf.MustMake("target"), sxpf.MakeString("_blank")),
				),
				sxpf.MakeString("\u266e"),
			),
		))
	return slideHtml
}

func getJSFileScript(src string, sf sxpf.SymbolFactory) *sxpf.List {
	return sxpf.MakeList(
		sf.MustMake("script"),
		sxpf.MakeList(
			sf.MustMake(sxhtml.NameSymAttr),
			sxpf.Cons(sf.MustMake("src"), sxpf.MakeString(src)),
		),
	)
}

type handoutRenderer struct{ cfg *slidesConfig }

func (*handoutRenderer) Role() string            { return SlideRoleHandout }
func (*handoutRenderer) Prepare(context.Context) {}
func (hr *handoutRenderer) Render(w http.ResponseWriter, slides *slideSet, author string) {
	sf := sxpf.MakeMappedFactory()
	symAttr := sf.MustMake(sxhtml.NameSymAttr)
	gen := newGenerator(sf, slides, hr, false, true)

	handoutTitle := slides.Title(hr.cfg.zs)
	copyright := slides.Copyright()
	license := slides.License()

	const extraCss = `blockquote {
  border-left: 0.5rem solid lightgray;
  padding-left: 1rem;
  margin-left: 1rem;
  margin-right: 2rem;
  font-style: italic;
}
blockquote p { margin-bottom: .5rem }
blockquote cite { font-style: normal }
aside.handout { border: 0.2rem solid lightgray }
`
	headHtml := getHTMLHead(extraCss, sf)
	headHtml.LastPair().AppendBang(getSimpleMeta("author", author, sf)).
		AppendBang(getSimpleMeta("copyright", copyright, sf)).
		AppendBang(getSimpleMeta("license", license, sf)).
		AppendBang(sxpf.MakeList(sf.MustMake("title"), sxpf.MakeString(text.EvaluateInlineString(handoutTitle))))

	offset := 1
	lang := slides.Lang()
	headerHtml := sxpf.MakeList(sf.MustMake("header"))
	if handoutTitle != nil {
		offset++
		curr := sxpf.MakeList(sf.MustMake("hgroup"))
		headerHtml.LastPair().AppendBang(curr)
		curr = curr.AppendBang(
			gen.Transform(handoutTitle).
				Cons(sxpf.MakeList(symAttr, sxpf.Cons(sf.MustMake("id"), sxpf.MakeString("(1)")))).
				Cons(sf.MustMake("h1")))
		if handoutSubtitle := slides.Subtitle(); handoutSubtitle != nil {
			curr = curr.AppendBang(gen.Transform(handoutSubtitle).Cons(sf.MustMake("h2")))
		}
		curr.AppendBang(sxpf.MakeList(sf.MustMake("p"), sxpf.MakeString(author))).
			AppendBang(sxpf.MakeList(sf.MustMake("p"), sxpf.MakeString(copyright))).
			AppendBang(sxpf.MakeList(sf.MustMake("p"), sxpf.MakeString(license)))
	}
	articleHtml := sxpf.MakeList(sf.MustMake("article"))
	curr := articleHtml
	for si := slides.Slides(SlideRoleHandout, offset); si != nil; si = si.Next() {
		gen.SetCurrentSlide(si)
		gen.SetUnique(fmt.Sprintf("%d:", si.Number))
		idAttr := sxpf.MakeList(
			symAttr,
			sxpf.Cons(sf.MustMake("id"), sxpf.MakeString(fmt.Sprintf("(%d)", si.Number))),
		)
		sl := si.Slide
		if slideTitle := sl.title; slideTitle != nil {
			h1 := sxpf.MakeList(sf.MustMake("h1"), idAttr)
			h1.LastPair().ExtendBang(gen.Transform(slideTitle)).AppendBang(getSlideNoRange(si, sf))
			curr = curr.AppendBang(h1)
		} else {
			curr = curr.AppendBang(sxpf.MakeList(sf.MustMake("a"), idAttr))
		}
		content := gen.Transform(sl.content)
		if slLang := sl.lang; slLang != "" && slLang != lang {
			content = content.Cons(sxpf.MakeList(symAttr, sxpf.Cons(sf.MustMake("lang"), sxpf.MakeString(slLang)))).Cons(sf.MustMake("div"))
			curr = curr.AppendBang(content)
		} else {
			curr = curr.ExtendBang(content)
		}
	}
	footerHtml := sxpf.MakeList(sf.MustMake("footer"), gen.Endnotes())
	bodyHtml := sxpf.MakeList(sf.MustMake("body"), headerHtml, articleHtml, footerHtml)
	gen.writeHTMLDocument(w, lang, headHtml, bodyHtml)
}

func getSlideNoRange(si *slideInfo, sf sxpf.SymbolFactory) *sxpf.List {
	if fromSlideNo := si.SlideNo; fromSlideNo > 0 {
		lstSlNo := sxpf.MakeList(sf.MustMake(sxhtml.NameSymNoEscape))
		if toSlideNo := si.LastChild().SlideNo; fromSlideNo < toSlideNo {
			lstSlNo.AppendBang(sxpf.MakeString(fmt.Sprintf(" (S.%d&ndash;%d)", fromSlideNo, toSlideNo)))
		} else {
			lstSlNo.AppendBang(sxpf.MakeString(fmt.Sprintf(" (S.%d)", fromSlideNo)))
		}
		return sxpf.MakeList(sf.MustMake("small"), lstSlNo)
	}
	return nil
}

func setupSlideSet(slides *slideSet, l []api.ZidMetaJSON, getZettel getZettelContentFunc, sGetZettel sGetZettelFunc, zs *sz.ZettelSymbols) {
	for _, sl := range l {
		slides.AddSlide(sl.ID, sGetZettel, zs)
	}
	slides.Completion(getZettel, sGetZettel, zs)
}

func processList(w http.ResponseWriter, r *http.Request, c *client.Client, astSF sxpf.SymbolFactory, zs *sz.ZettelSymbols) {
	ctx := r.Context()
	_, human, zl, err := c.ListZettelJSON(ctx, strings.Join(r.URL.Query()[api.QueryKeyQuery], " "))
	if err != nil {
		http.Error(w, fmt.Sprintf("Error retrieving zettel list %s: %s\n", r.URL.Query(), err), http.StatusBadRequest)
		return
	}
	log.Println("LIST", human, zl)

	sf := sxpf.MakeMappedFactory()
	gen := newGenerator(sf, nil, nil, false, false)

	titles := make([]*sxpf.List, len(zl))
	for i, jm := range zl {
		if sMeta, err2 := c.GetEvaluatedSz(ctx, jm.ID, api.PartMeta, astSF); err2 == nil {
			titles[i] = gen.Transform(getZettelTitleZid(sz.MakeMeta(sMeta), jm.ID, zs))
		}
	}

	var title string
	if human == "" {
		title = "All zettel"
		human = title
	} else {
		title = "Selected zettel"
		human = "Search: " + human
	}

	headHtml := getHTMLHead("", sf)
	headHtml.LastPair().AppendBang(sxpf.MakeList(sf.MustMake("title"), sxpf.MakeString(title)))

	ul := sxpf.MakeList(sf.MustMake("ul"))
	curr := ul.LastPair()
	for i, jm := range zl {
		curr = curr.AppendBang(sxpf.MakeList(
			sf.MustMake("li"), getSimpleLink(string(jm.ID), titles[i], sf),
		))
	}
	bodyHtml := sxpf.MakeList(sf.MustMake("body"), sxpf.MakeList(sf.MustMake("h1"), sxpf.MakeString(human)), ul)
	gen.writeHTMLDocument(w, "", headHtml, bodyHtml)
}

func getHTMLHead(extraCss string, sf sxpf.SymbolFactory) *sxpf.List {
	symAttr := sf.MustMake(sxhtml.NameSymAttr)
	return sxpf.MakeList(
		sf.MustMake("head"),
		sxpf.MakeList(sf.MustMake("meta"), sxpf.MakeList(symAttr, sxpf.Cons(sf.MustMake("charset"), sxpf.MakeString("utf-8")))),
		sxpf.MakeList(sf.MustMake("meta"), sxpf.MakeList(
			symAttr,
			sxpf.Cons(sf.MustMake("name"), sxpf.MakeString("viewport")),
			sxpf.Cons(sf.MustMake("content"), sxpf.MakeString("width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no")),
		)),
		sxpf.MakeList(sf.MustMake("meta"), sxpf.MakeList(
			symAttr,
			sxpf.Cons(sf.MustMake("name"), sxpf.MakeString("generator")),
			sxpf.Cons(sf.MustMake("content"), sxpf.MakeString("Zettel Presenter")),
		)),
		getPrefixedCSS("", extraCss, sf),
	)
}

var defaultCSS = []string{
	"td.left,",
	"th.left { text-align: left }",
	"td.center,",
	"th.center { text-align: center }",
	"td.right,",
	"th.right { text-align: right }",
	"ol.zs-endnotes { padding-top: .5rem; border-top: 1px solid; font-size: smaller; margin-left: 2em; }",
	`a.external::after { content: "➚"; display: inline-block }`,
	`a.zettel::after { content: "⤳"; display: inline-block }`,
	"a.broken { text-decoration: line-through }",
}

func getPrefixedCSS(prefix string, extraCss string, sf sxpf.SymbolFactory) *sxpf.List {
	var result *sxpf.List
	if extraCss != "" {
		result = result.Cons(sxpf.MakeString(extraCss))
	}
	symHTML := sf.MustMake("@H")
	for i := range defaultCSS {
		result = result.Cons(sxpf.MakeList(symHTML, sxpf.MakeString(prefix+defaultCSS[len(defaultCSS)-i-1]+"\n")))
	}
	return result.Cons(sf.MustMake("style"))
}

func getSimpleLink(url string, text *sxpf.List, sf sxpf.SymbolFactory) *sxpf.List {
	result := sxpf.MakeList(
		sf.MustMake("a"),
		sxpf.MakeList(
			sf.MustMake(sxhtml.NameSymAttr),
			sxpf.Cons(sf.MustMake("href"), sxpf.MakeString(url)),
		),
	)
	curr := result.LastPair()
	for elem := text; elem != nil; elem = elem.Tail() {
		curr = curr.AppendBang(elem.Car())
	}
	return result
}

func getSimpleMeta(key, val string, sf sxpf.SymbolFactory) *sxpf.List {
	return sxpf.MakeList(
		sf.MustMake("meta"),
		sxpf.MakeList(
			sf.MustMake(sxhtml.NameSymAttr),
			sxpf.Cons(sf.MustMake(key), sxpf.MakeString(val)),
		),
	)
}

func getHeadLink(rel, href string, sf sxpf.SymbolFactory) *sxpf.List {
	return sxpf.MakeList(
		sf.MustMake("link"),
		sxpf.MakeList(
			sf.MustMake(sxhtml.NameSymAttr),
			sxpf.Cons(sf.MustMake("rel"), sxpf.MakeString(rel)),
			sxpf.Cons(sf.MustMake("href"), sxpf.MakeString(href)),
		))
}

func getClassAttr(class string, sf sxpf.SymbolFactory) *sxpf.List {
	return sxpf.MakeList(
		sf.MustMake(sxhtml.NameSymAttr),
		sxpf.Cons(sf.MustMake("class"), sxpf.MakeString(class)),
	)
}

//go:embed revealjs
var revealjs embed.FS
