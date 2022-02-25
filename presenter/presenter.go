//-----------------------------------------------------------------------------
// Copyright (c) 2021-2022 Detlef Stern
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
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/term"

	"zettelstore.de/c/api"
	"zettelstore.de/c/client"
	"zettelstore.de/c/zjson"
)

func main() {
	withAuth := flag.Bool("a", false, "Zettelstore needs authentication")
	listenAddress := flag.String("l", ":23120", "Listen address")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		io.WriteString(out, "  [URL] URL of Zettelstore (default: \"http://127.0.0.1:23123\")\n")
	}
	flag.Parse()
	ctx := context.Background()
	c, err := getClient(ctx, flag.Arg(0), *withAuth)
	if err != nil {
		panic(err)
	}
	cfg, err := getConfig(ctx, c)
	if err != nil {
		panic(err)
	}

	// Fix an error in slidy.js
	slidy2js = strings.ReplaceAll(slidy2js, "</script>", "<\\/script>")

	http.HandleFunc("/", makeHandler(&cfg))
	fmt.Println("Listening:", *listenAddress)
	http.ListenAndServe(*listenAddress, nil)
}

func getClient(ctx context.Context, base string, withAuth bool) (*client.Client, error) {
	if base == "" {
		base = "http://127.0.0.1:23123"
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	username, password := "", ""
	if uinfo := u.User; uinfo != nil {
		username = uinfo.Username()
		if pw, ok := uinfo.Password(); ok {
			password = pw
		}
		withAuth = true
	}
	c := client.NewClient(base)
	if withAuth {
		if username == "" {
			io.WriteString(os.Stderr, "Username: ")
			_, err := fmt.Fscanln(os.Stdin, &username)
			if err != nil {
				return nil, err
			}
		}
		if password == "" {
			io.WriteString(os.Stderr, "Password: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			io.WriteString(os.Stderr, "\n")
			if err != nil {
				return nil, err
			}
			password = string(pw)
		}
		c.SetAuth(username, password)
		err := c.Authenticate(ctx)
		if err != nil {
			return nil, err
		}
	}

	return c, nil
}

const configZettel = api.ZettelID("00009000001000")

type slidesConfig struct {
	c            *client.Client
	slideSetRole string
	author       string
	copyright    string
}

func getConfig(ctx context.Context, c *client.Client) (slidesConfig, error) {
	result := slidesConfig{
		c:            c,
		slideSetRole: "slideset",
	}
	m, err := c.GetMeta(ctx, configZettel)
	if err != nil {
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
			return result, nil
		}
		panic(err)
	}
	if ssr, ok := m["slideset-role"]; ok {
		result.slideSetRole = ssr
	}
	if author, ok := m["author"]; ok {
		result.author = author
	}
	if copyright, ok := m["copyright"]; ok {
		result.copyright = copyright
	}
	return result, nil
}

func makeHandler(cfg *slidesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			processZettel(w, r, cfg.c, api.ZidDefaultHome, cfg.slideSetRole)
			return
		}
		if zid := api.ZettelID(path[1:]); zid.IsValid() {
			processZettel(w, r, cfg.c, zid, cfg.slideSetRole)
			return
		}
		if strings.HasPrefix(path, "/sl/") {
			if zid := api.ZettelID(path[4:]); zid.IsValid() {
				processSlideSet(w, r, cfg, zid)
				return
			}
		}
		if strings.HasPrefix(path, "/z/") {
			if zid := api.ZettelID(path[3:]); zid.IsValid() {
				processContent(w, r, cfg.c, zid)
				return
			}
		}
		if len(path) == 2 && ' ' < path[1] && path[1] <= 'z' {
			processList(w, r, cfg.c)
			return
		}
		http.Error(w, fmt.Sprintf("Unhandled request %q", r.URL), http.StatusNotFound)
	}
}

func processContent(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID) {
	content, err := c.GetZettel(r.Context(), zid, api.PartContent)
	if err != nil {
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
			http.Error(w, fmt.Sprintf("Content %s not found", zid), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Error retrieving content %s: %s", zid, err), http.StatusBadRequest)
		}
	}
	w.Write(content)
}

func processZettel(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID, slidesRole string) {
	ctx := r.Context()
	zjZettel, err := c.GetEvaluatedZJSON(ctx, zid, api.PartZettel)
	if err != nil {
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
			http.Error(w, fmt.Sprintf("Zettel %s not found", zid), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Error retrieving zettel %s: %s", zid, err), http.StatusBadRequest)
		}
		return
	}
	m, content := zjson.GetMetaContent(zjZettel)
	if m == nil || content == nil {
		http.Error(w, fmt.Sprintf("Zettel %s has no meta/content", zid), http.StatusInternalServerError)
		return
	}
	role := m.GetString(api.KeyRole)
	if role == slidesRole && writeSlideTOC(ctx, w, c, zid, m, content) {
		return
	}

	writeHTMLZettel(ctx, w, c, zid, m, content)
}

func writeSlideTOC(ctx context.Context, w http.ResponseWriter, c *client.Client, zid api.ZettelID, m zjson.Meta, content zjson.Array) bool {
	o, err := c.GetZettelOrder(ctx, zid)
	if err != nil {
		return false
	}
	offset, title, subtitle := 1, getSlideTitle(m), m.GetArray("sub-title")
	if len(title) > 0 {
		offset++
	}

	writeHTMLHeader(w, m.GetString(api.KeyLang))
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", textEncodeInline(title))
	}
	writeHTMLBody(w)
	if len(title) > 0 {
		fmt.Fprintf(w, "<h1>%s</h1>\n", htmlEncodeInline(title))
		if len(subtitle) > 0 {
			fmt.Fprintf(w, "<h2>%s</h2>\n", htmlEncodeInline(subtitle))
		}
	}
	fmt.Fprintf(w, "<p><a href=\"/sl/%s\">Start</a></p>\n", zid)
	io.WriteString(w, "<ol>\n")
	for i, sl := range o.List {
		var slideTitle string
		if slMeta, err := c.GetEvaluatedZJSON(ctx, sl.ID, api.PartMeta); err == nil {
			slm := zjson.MakeMeta(slMeta)
			slideTitle = htmlEncodeInline(getSlideTitle(slm))
		} else {
			slideTitle = string(sl.ID)
		}
		fmt.Fprintf(
			w,
			"<li><a href=\"/sl/%s#(%d)\">%s</a></li>\n",
			zid,
			i+offset,
			slideTitle,
		)
	}
	io.WriteString(w, "</ol>\n")
	writeHTMLFooter(w)
	return true
}

func writeHTMLZettel(ctx context.Context, w http.ResponseWriter, c *client.Client, zid api.ZettelID, m zjson.Meta, content zjson.Array) {
	// zjZettel, err := c.GetEvaluatedZJSON(ctx, zid, api.PartZettel)
	// if err != nil {
	// 	var cerr *client.Error
	// 	if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
	// 		http.Error(w, fmt.Sprintf("Zettel %s not found", zid), http.StatusNotFound)
	// 	} else {
	// 		http.Error(w, fmt.Sprintf("Error retrieving parsed zettel %s: %s", zid, err), http.StatusBadRequest)
	// 	}
	// 	return
	// }
	title := getSlideTitleZid(m, zid)
	writeHTMLHeader(w, m.GetString(api.KeyLang))
	fmt.Fprintf(w, "<title>%s</title>\n", textEncodeInline(title))
	writeHTMLBody(w)
	fmt.Fprintf(w, "<h1>%s</h1>\n", htmlEncodeInline(title))

	he := htmlNew(w, 1, "")
	zjson.WalkBlock(he, content, 0)
	he.visitEndnotes()

	writeHTMLFooter(w)
}

func processSlideSet(w http.ResponseWriter, r *http.Request, cfg *slidesConfig, zid api.ZettelID) {
	ctx := r.Context()
	o, err := cfg.c.GetZettelOrder(ctx, zid)
	if err != nil {
		var cerr *client.Error
		if errors.As(err, &cerr) && cerr.StatusCode == http.StatusNotFound {
			http.Error(w, fmt.Sprintf("Zettel %s not found", zid), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Unable to read zettel %s: %v", zid, err), http.StatusBadRequest)
		}
		return
	}
	zjMeta, err := cfg.c.GetEvaluatedZJSON(ctx, zid, api.PartMeta)
	if err != nil {
		http.Error(w, fmt.Sprintf("Unable to read zettel %s: %v", zid, err), http.StatusBadRequest)
	}
	m := zjson.MakeMeta(zjMeta)
	title, subtitle := getSlideTitle(m), m.GetArray("sub-title")

	lang := m.GetString(api.KeyLang)
	writeHTMLHeader(w, lang)
	if len(title) > 0 {
		fmt.Fprintf(w, "<title>%s</title>\n", textEncodeInline(title))
	}
	if copyright := getCopyright(cfg, m); copyright != "" {
		fmt.Fprintf(w, "<meta name=\"copyright\" content=\"%s\" />\n", html.EscapeString(copyright))
	}
	fmt.Fprintf(w, "<style type=\"text/css\" media=\"screen, projection, print\">\n%s\n%s</style>\n", slidy2css, mycss)
	writeHTMLBody(w)

	if len(title) > 0 {
		io.WriteString(w, "<div class=\"slide titlepage\">\n")
		fmt.Fprintf(w, "<h1 class=\"title\">%s</h1>\n", htmlEncodeInline(title))
		if len(subtitle) > 0 {
			fmt.Fprintf(w, "<p class=\"subtitle\">%s</p>\n", htmlEncodeInline(subtitle))
		}
		if author := getAuthor(cfg, m); author != "" {
			fmt.Fprintf(w, "<p class=\"author\">%s</p>\n", html.EscapeString(author))
		}
		io.WriteString(w, "\n</div>\n")
	}
	for slideNo, sl := range o.List {
		zjZettel, err := cfg.c.GetEvaluatedZJSON(ctx, sl.ID, api.PartZettel)
		if err != nil {
			continue
		}
		slMeta, slContent := zjson.GetMetaContent(zjZettel)
		if slMeta == nil || slContent == nil {
			continue
		}
		io.WriteString(w, `<div class="slide"`)
		if slLang := slMeta.GetString(api.KeyLang); slLang != "" && slLang != lang {
			fmt.Fprintf(w, ` lang="%s"`, slLang)
		}
		io.WriteString(w, ">\n")
		if title := getSlideTitle(slMeta); len(title) > 0 {
			fmt.Fprintf(w, "<h1>%s</h1>\n", htmlEncodeInline(title))
		}

		he := htmlNew(w, 1, fmt.Sprintf("%d:", slideNo))
		zjson.WalkBlock(he, slContent, 0)
		he.visitEndnotes()
		io.WriteString(w, "</div>\n")
	}
	fmt.Fprintf(w, "<script type=\"text/javascript\">\n//<![CDATA[\n%s//]]>\n</script>\n", slidy2js)
	writeHTMLFooter(w)
}

func processList(w http.ResponseWriter, r *http.Request, c *client.Client) {
	ctx := r.Context()
	query, zl, err := c.ListZettelJSON(ctx, r.URL.Query())
	if err != nil {
		http.Error(w, fmt.Sprintf("Error retrieving zettel list %s: %s\n", r.URL.Query(), err), http.StatusBadRequest)
		return
	}
	titles := make([]string, len(zl))
	for i, jm := range zl {
		if zjMeta, err := c.GetEvaluatedZJSON(ctx, jm.ID, api.PartMeta); err == nil {
			titles[i] = htmlEncodeInline(getZettelTitleZid(zjson.MakeMeta(zjMeta), jm.ID))
		}
	}

	var title string
	if query == "" {
		title = "All zettel"
		query = title
	} else {
		title = "Selected zettel"
		query = "Search: " + query
	}
	writeHTMLHeader(w, "")
	fmt.Fprintf(w, "<title>%s</title>\n", title)
	writeHTMLBody(w)
	fmt.Fprintf(w, "<h1>%s</h1>\n", html.EscapeString(query))
	io.WriteString(w, "<ul>\n")
	for i, jm := range zl {
		fmt.Fprintf(
			w,
			"<li><a href=\"%s\">%s</a></li>\n",
			jm.ID,
			titles[i],
		)
	}
	io.WriteString(w, "</ul>\n")
	writeHTMLFooter(w)
}

func writeHTMLHeader(w http.ResponseWriter, lang string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!DOCTYPE html>\n")
	if lang == "" {
		io.WriteString(w, "<html>\n")
	} else {
		fmt.Fprintf(w, "<html lang=\"%s\">\n", lang)
	}
	io.WriteString(w, "<head>\n")
	fmt.Fprintf(w, "<style type=\"text/css\" media=\"screen, projection, print\">\n%s</style>\n", mycss)
}

func writeHTMLBody(w http.ResponseWriter) {
	io.WriteString(w, "</head>\n<body>\n")
}
func writeHTMLFooter(w http.ResponseWriter) {
	io.WriteString(w, "</body>\n</html>\n")
}

func getSlideTitle(m zjson.Meta) zjson.Array {
	if title := m.GetArray("slide-title"); len(title) > 0 {
		return title
	}
	return m.GetArray(api.KeyTitle)
}

func getSlideTitleZid(m zjson.Meta, zid api.ZettelID) zjson.Array {
	if title := getSlideTitle(m); len(title) > 0 {
		return title
	}
	return zjson.Array{zjson.Object{zjson.NameType: zjson.TypeText, zjson.NameString: string(zid)}}
}

func getZettelTitleZid(m zjson.Meta, zid api.ZettelID) zjson.Array {
	if title := m.GetArray(api.KeyTitle); len(title) > 0 {
		return title
	}
	return zjson.Array{zjson.Object{zjson.NameType: zjson.TypeText, zjson.NameString: string(zid)}}
}

func getAuthor(cfg *slidesConfig, m zjson.Meta) string {
	if author := m.GetString("author"); author != "" {
		return author
	}
	return cfg.author
}
func getCopyright(cfg *slidesConfig, m zjson.Meta) string {
	if copyright := m.GetString("copyright"); copyright != "" {
		return copyright
	}
	return cfg.copyright
}

//go:embed slidy2/slidy.css
var slidy2css string

//go:embed slidy2/slidy.js
var slidy2js string

var mycss = `/* Additional CSS to make it a little more beautiful */
.zp-left { text-align: left }
.zp-center { text-align: center }
.zp-right { text-align: right }
.zp-endnotes { padding-top: .5rem; border-top: 1px solid }
.zp-external {}
.zp-broken { text-decoration: line-through }
`

// Everything below this line should move into client/zjson
