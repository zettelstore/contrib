//-----------------------------------------------------------------------------
// Copyright (c) 2021 Detlef Stern
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
	jz, err := c.GetZettelJSON(ctx, configZettel)
	if err != nil {
		return result, nil // TODO: check 404 vs other codes
	}
	m := jz.Meta
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
		if len(path) == 2 && ' ' < path[1] && path[1] <= 'z' {
			processList(w, r, cfg.c)
			return
		}
		http.Error(w, fmt.Sprintf("Unhandled request %q", r.URL), http.StatusNotFound)
	}
}

func processZettel(w http.ResponseWriter, r *http.Request, c *client.Client, zid api.ZettelID, slidesRole string) {
	ctx := r.Context()
	jz, err := c.GetZettelJSON(ctx, zid)
	if err != nil {
		fmt.Fprintf(w, "Error retrieving zettel %s: %s\n", zid, err)
		return
	}
	m := jz.Meta
	role := m[api.KeyRole]
	if role == slidesRole && writeSlideTOC(ctx, w, c, zid) {
		return
	}

	writeHTMLZettel(ctx, w, c, zid, m)
}

func writeSlideTOC(ctx context.Context, w http.ResponseWriter, c *client.Client, zid api.ZettelID) bool {
	o, err := c.GetZettelOrder(ctx, zid)
	if err != nil {
		return false
	}
	m := o.Meta
	offset, title, subtitle := 1, getTitle(m), m["subtitle"]
	if title != "" {
		offset++
	}
	writeHTMLHeader(w, m[api.KeyLang])
	io.WriteString(w, "<title>TODO: TOC Slide</title>\n")
	writeHTMLBody(w)
	if title != "" {
		fmt.Fprintf(w, "<h1>%s</h1>\n", html.EscapeString(title))
		if subtitle != "" {
			fmt.Fprintf(w, "<h2>%s</h2>\n", html.EscapeString(subtitle))
		}
	}
	// TODO: io.WriteString(w, "<p>TODO: Initial content</p>\n")
	fmt.Fprintf(w, "<p><a href=\"/sl/%s\">Start</a></p>\n", zid)
	io.WriteString(w, "<ol>\n")
	for i, sl := range o.List {
		fmt.Fprintf(
			w,
			"<li><a href=\"/sl/%s#(%d)\">%s</a></li>\n",
			zid,
			i+offset,
			html.EscapeString(getTitleZid(sl.Meta, sl.ID)),
		)
	}
	io.WriteString(w, "</ol>\n")
	writeHTMLFooter(w)
	return true
}

func writeHTMLZettel(ctx context.Context, w http.ResponseWriter, c *client.Client, zid api.ZettelID, m map[string]string) {
	content, err := c.GetParsedZettel(ctx, zid, api.EncoderHTML)
	if err != nil {
		fmt.Fprintf(w, "Error retrieving parsed zettel %s: %s\n", zid, err)
		return
	}
	writeHTMLHeader(w, m[api.KeyLang])
	io.WriteString(w, "<title>TODO: Title Zettel</title>\n")
	writeHTMLBody(w)
	io.WriteString(w, "<h1>TODO: Title Zettel</h1>\n")
	fmt.Fprint(w, content)
	writeHTMLFooter(w)
}

func processSlideSet(w http.ResponseWriter, r *http.Request, cfg *slidesConfig, zid api.ZettelID) {
	ctx := r.Context()
	o, err := cfg.c.GetZettelOrder(ctx, zid)
	if err != nil {
		writeHTMLZettel(ctx, w, cfg.c, zid, map[string]string{})
		return
	}
	m := o.Meta
	writeHTMLHeader(w, m[api.KeyLang])
	title, subtitle := getTitle(m), m["subtitle"]
	io.WriteString(w, "<title>TODO: Title Slides</title>\n")
	if copyright := getCopyright(cfg, m); copyright != "" {
		fmt.Fprintf(w, "<meta name=\"copyright\" content=\"%s\" />\n", html.EscapeString(copyright))
	}
	fmt.Fprintf(w, "<style type=\"text/css\" media=\"screen, projection, print\">\n%s</style>\n", slidy2css)
	writeHTMLBody(w)

	if title != "" {
		io.WriteString(w, "<div class=\"slide titlepage\">\n")
		fmt.Fprintf(w, "<h1 class=\"title\">%s</h1>\n", html.EscapeString(title))
		if subtitle != "" {
			fmt.Fprintf(w, "<p class=\"subtitle\">%s</p>\n", html.EscapeString(subtitle))
		}
		if author := getAuthor(cfg, m); author != "" {
			fmt.Fprintf(w, "<p class=\"author\">%s</p>\n", html.EscapeString(author))
		}
		io.WriteString(w, "\n</div>\n")
	}
	for _, sl := range o.List {
		content, err := cfg.c.GetParsedZettel(ctx, sl.ID, api.EncoderHTML)
		if err != nil {
			continue
		}
		io.WriteString(w, "<div class=\"slide\">\n")
		if title := getTitle(sl.Meta); title != "" {
			fmt.Fprintf(w, "<h1>%s</h1>\n", html.EscapeString(title))
		}
		io.WriteString(w, content)
		io.WriteString(w, "</div>\n")
	}
	fmt.Fprintf(w, "<script type=\"text/javascript\">\n//<![CDATA[\n%s//]]>\n</script>\n", slidy2js)
	writeHTMLFooter(w)
}

func processList(w http.ResponseWriter, r *http.Request, c *client.Client) {
	zl, err := c.ListZettelJSON(r.Context(), r.URL.Query())
	if err != nil {
		fmt.Fprintf(w, "Error retrieving zettel list %s: %s\n", r.URL.Query(), err)
		return
	}
	writeHTMLHeader(w, "")
	io.WriteString(w, "<title>TODO: Title List</title>\n")
	writeHTMLBody(w)
	io.WriteString(w, "<h1>TODO: Title List</h1>\n")
	io.WriteString(w, "<ul>\n")
	for _, jm := range zl {
		fmt.Fprintf(
			w,
			"<li><a href=\"%s\">%s</a></li>\n",
			jm.ID,
			html.EscapeString(getRealTitleZid(jm.Meta, jm.ID)),
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
}

func writeHTMLBody(w http.ResponseWriter) {
	io.WriteString(w, "</head>\n<body>\n")
}
func writeHTMLFooter(w http.ResponseWriter) {
	io.WriteString(w, "</body>\n</html>\n")
}

func getTitle(m map[string]string) string {
	if title := m["slidetitle"]; title != "" {
		return title
	}
	return m[api.KeyTitle]
}

func getTitleZid(m map[string]string, zid api.ZettelID) string {
	if title := getTitle(m); title != "" {
		return title
	}
	return string(zid)
}
func getRealTitleZid(m map[string]string, zid api.ZettelID) string {
	if title := m[api.KeyTitle]; title != "" {
		return title
	}
	return string(zid)
}

func getAuthor(cfg *slidesConfig, m map[string]string) string {
	if author := m["author"]; author != "" {
		return author
	}
	return cfg.author
}
func getCopyright(cfg *slidesConfig, m map[string]string) string {
	if copyright := m["copyright"]; copyright != "" {
		return copyright
	}
	return cfg.copyright
}

//go:embed slidy2/slidy.css
var slidy2css string

//go:embed slidy2/slidy.js
var slidy2js string
