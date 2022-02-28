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
	"zettelstore.de/c/api"
	"zettelstore.de/c/zjson"
)

// Slide is one slide that is shown one or more times.
type slide struct {
	zid     api.ZettelID // The zettel identifier
	meta    zjson.Meta   // Metadata of this slide
	content zjson.Array  // Zettel / slide content
}

func (s *slide) Title() zjson.Array   { return getSlideTitle(s.meta) }
func (s *slide) Lang() string         { return s.meta.GetString(api.KeyLang) }
func (s *slide) Content() zjson.Array { return s.content }

// slideSet is the sequence of slides shown.
type slideSet struct {
	zid       api.ZettelID
	meta      zjson.Meta // Metadata of slideset
	slides    []*slide
	completed bool
}

func newSlideSet(zid api.ZettelID, zjMeta zjson.Value) *slideSet {
	m := zjson.MakeMeta(zjMeta)
	if len(m) == 0 {
		return nil
	}
	return newSlideSetMeta(zid, m)
}
func newSlideSetMeta(zid api.ZettelID, m zjson.Meta) *slideSet {
	return &slideSet{
		zid:  zid,
		meta: m,
	}
}

func (s *slideSet) Slides() []*slide { return s.slides }

func (s *slideSet) Title() zjson.Array { return getSlideTitle(s.meta) }
func (s *slideSet) Subtitle() zjson.Array {
	if subTitle := s.meta.GetArray("sub-title"); len(subTitle) > 0 {
		return subTitle
	}
	return nil
}
func (s *slideSet) Lang() string { return s.meta.GetString(api.KeyLang) }
func (s *slideSet) Author(cfg *slidesConfig) string {
	if author := s.meta.GetString("author"); author != "" {
		return author
	}
	return cfg.author
}
func (s *slideSet) Copyright(cfg *slidesConfig) string {
	if copyright := s.meta.GetString("copyright"); copyright != "" {
		return copyright
	}
	return cfg.copyright
}

func (s *slideSet) AddSlide(zid api.ZettelID, zjZettel zjson.Value) {
	for _, sl := range s.slides {
		if sl.zid == zid {
			s.slides = append(s.slides, sl)
			return
		}
	}
	slMeta, slContent := zjson.GetMetaContent(zjZettel)
	if slMeta == nil || slContent == nil {
		// TODO: Add artificial slide with error message
		return
	}
	sl := &slide{
		zid:     zid,
		meta:    slMeta,
		content: slContent,
	}
	s.slides = append(s.slides, sl)
}

func (s *slideSet) AddError(data api.ZidMetaJSON, err error) {
	// TODO: add artificial slide with error data
}

func (s *slideSet) Completion() {
	if s.completed {
		return
	}
	// TODO: complete the slide set
	s.completed = true
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
