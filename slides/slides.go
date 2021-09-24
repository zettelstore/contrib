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
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"

	"zettelstore.de/z/api"
	"zettelstore.de/z/client"
	"zettelstore.de/z/domain/id"
	"zettelstore.de/z/domain/meta"
)

func main() {
	withAuth := flag.Bool("a", false, "Zettelstore needs authentication")
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
	http.HandleFunc("/", makeHandler(c, &cfg))
	fmt.Println("Listening:", cfg.listenAddr)
	http.ListenAndServe(cfg.listenAddr, nil)
}

func getClient(ctx context.Context, base string, withauth bool) (*client.Client, error) {
	if base == "" {
		base = "http://127.0.0.1:23123"
	}
	c := client.NewClient(base)
	return c, nil
}

const configZettel = id.Zid(9000001000)

type slidesConfig struct {
	listenAddr   string
	slideSetRole string
}

func getConfig(ctx context.Context, c *client.Client) (slidesConfig, error) {
	result := slidesConfig{
		listenAddr:   ":29549",
		slideSetRole: "slideset",
	}
	jz, err := c.GetZettelJSON(ctx, configZettel)
	if err != nil {
		return result, nil // TODO: check 404 vs other codes
	}
	if la, ok := jz.Meta["listen-addr"]; ok {
		result.listenAddr = la
	}
	if ssr, ok := jz.Meta["slideset-role"]; ok {
		result.slideSetRole = ssr
	}
	return result, nil
}

func makeHandler(c *client.Client, cfg *slidesConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			processZettel(w, r, c, id.DefaultHomeZid, cfg.slideSetRole)
			return
		}
		if zid, err := id.Parse(path[1:]); err == nil {
			processZettel(w, r, c, zid, cfg.slideSetRole)
			return
		}
		if strings.HasPrefix(path, "/sl/") {
			if zid, err := id.Parse(path[4:]); err == nil {
				processSlideSet(w, r, c, zid)
				return
			}
		}
		if len(path) == 2 && ' ' < path[1] && path[1] <= 'z' {
			processList(w, r, c)
			return
		}
		http.Error(w, fmt.Sprintf("Unhandled request %q", r.URL), http.StatusNotFound)
	}
}

func processZettel(w http.ResponseWriter, r *http.Request, c *client.Client, zid id.Zid, slidesRole string) {
	ctx := r.Context()
	jz, err := c.GetZettelJSON(ctx, zid)
	if err != nil {
		fmt.Fprintf(w, "Error retrieving zettel %s: %s\n", zid, err)
		return
	}
	role := jz.Meta[meta.KeyRole]
	if role == slidesRole {
		writeSlideTOC(ctx, w, c, zid)
		return
	}

	writeHTMLZettel(ctx, w, c, zid)
}

func writeSlideTOC(ctx context.Context, w http.ResponseWriter, c *client.Client, zid id.Zid) {
	o, err := c.GetZettelOrder(ctx, zid)
	if err != nil {
		writeHTMLZettel(ctx, w, c, zid)
		return
	}
	writeHTMLHeader(w)
	io.WriteString(w, "<title>TODO: TOC Slide</title>\n")
	writeHTMLBody(w)
	io.WriteString(w, "<h1>TODO: TOC Slides</h1>\n")
	io.WriteString(w, "<p>TODO: Initial content</p>\n")
	fmt.Fprintf(w, "<p><a href=\"/sl/%s\">Start</a></p>\n", zid)
	io.WriteString(w, "<ol>\n")
	for i, sl := range o.List {
		fmt.Fprintf(
			w,
			"<li><a href=\"/sl/%s#(%d)\">%s</a></li>\n",
			zid,
			i+1,
			html.EscapeString(getTitleZid(sl.Meta, sl.ID)),
		)
	}
	io.WriteString(w, "</ol>\n")
	writeHTMLFooter(w)
}

func writeHTMLZettel(ctx context.Context, w http.ResponseWriter, c *client.Client, zid id.Zid) {
	content, err := c.GetParsedZettel(ctx, zid, api.EncoderHTML)
	if err != nil {
		fmt.Fprintf(w, "Error retrieving parsed zettel %s: %s\n", zid, err)
		return
	}
	writeHTMLHeader(w)
	io.WriteString(w, "<title>TODO: Title Zettel</title>\n")
	writeHTMLBody(w)
	io.WriteString(w, "<h1>TODO: Title Zettel</h1>\n")
	fmt.Fprint(w, content)
	writeHTMLFooter(w)
}

func processSlideSet(w http.ResponseWriter, r *http.Request, c *client.Client, zid id.Zid) {
	ctx := r.Context()
	o, err := c.GetZettelOrder(ctx, zid)
	if err != nil {
		writeHTMLZettel(ctx, w, c, zid)
		return
	}
	writeHTMLHeader(w)
	io.WriteString(w, "<title>TODO: Title Slides</title>\n")
	if copyright := o.Meta[meta.KeyCopyright]; copyright != "" {
		fmt.Fprintf(w, "<meta name=\"copyright\" content=\"%s\" />\n", html.EscapeString(copyright))
	}
	io.WriteString(w, "<link rel=\"stylesheet\" type=\"text/css\" media=\"screen, projection, print\" href=\"http://www.w3.org/Talks/Tools/Slidy2/styles/slidy.css\" />\n")
	io.WriteString(w, "<script src=\"http://www.w3.org/Talks/Tools/Slidy2/scripts/slidy.js\" charset=\"utf-8\" type=\"text/javascript\"></script>\n")
	writeHTMLBody(w)

	if title := getTitle(o.Meta); title != "" {
		io.WriteString(w, "<div class=\"slide titlepage\">\n")
		fmt.Fprintf(w, "<h1 class=\"title\">%s</h1>\n", html.EscapeString(title))
		if subtitle := o.Meta["subtitle"]; subtitle != "" {
			fmt.Fprintf(w, "<p class=\"subtitle\">%s</p>\n", html.EscapeString(subtitle))
		}
		if author := o.Meta["author"]; author != "" {
			fmt.Fprintf(w, "<p class=\"author\">%s</p>\n", html.EscapeString(author))
		}
		io.WriteString(w, "\n</div>\n")
	}
	for _, sl := range o.List {
		slzid, _ := id.Parse(sl.ID)
		content, err := c.GetParsedZettel(ctx, slzid, api.EncoderHTML)
		if err != nil {
			continue
		}
		io.WriteString(w, "<div class=\"slide\">\n")
		if title := getTitle(sl.Meta); title != "" {
			fmt.Fprintf(w, "<h1>%s</h1>\n", html.EscapeString(title))
		}
		io.WriteString(w, content)
		io.WriteString(w, "\n</div>\n")
	}
	writeHTMLFooter(w)
}

func processList(w http.ResponseWriter, r *http.Request, c *client.Client) {
	zl, err := c.ListZettelJSON(r.Context(), r.URL.Query())
	if err != nil {
		fmt.Fprintf(w, "Error retrieving zettel list %s: %s\n", r.URL.Query(), err)
		return
	}
	writeHTMLHeader(w)
	io.WriteString(w, "<title>TODO: Title List</title>\n")
	writeHTMLBody(w)
	io.WriteString(w, "<h1>TODO: Title List</h1>\n")
	io.WriteString(w, "<ul>\n")
	for _, jm := range zl {
		fmt.Fprintf(
			w,
			"<li><a href=\"%s\">%s</a></li>\n",
			jm.ID,
			html.EscapeString(getTitleZid(jm.Meta, jm.ID)),
		)
	}
	io.WriteString(w, "</ul>\n")
	writeHTMLFooter(w)
}

func writeHTMLHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!DOCTYPE html>\n<html>\n<head>\n")
}

func writeHTMLBody(w http.ResponseWriter) {
	io.WriteString(w, "</head>\n<body>\n")
}
func writeHTMLFooter(w http.ResponseWriter) {
	io.WriteString(w, "</body>\n</html>\n")
}

func getTitle(m map[string]string) string {
	return m[meta.KeyTitle]
}

func getTitleZid(m map[string]string, zid string) string {
	if title := getTitle(m); title != "" {
		return title
	}
	return zid
}
