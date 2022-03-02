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
	"log"

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

type image struct {
	syntax string
	data   []byte
}

// slideSet is the sequence of slides shown.
type slideSet struct {
	zid       api.ZettelID
	meta      zjson.Meta // Metadata of slideset
	seqSlide  []*slide   // slide may occur more than once in seq, but should be stored only once
	setSlide  map[api.ZettelID]*slide
	setImage  map[api.ZettelID]image
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
		zid:      zid,
		meta:     m,
		setSlide: make(map[api.ZettelID]*slide),
		setImage: make(map[api.ZettelID]image),
	}
}

func (s *slideSet) GetSlide(zid api.ZettelID) *slide {
	if sl, found := s.setSlide[zid]; found {
		return sl
	}
	return nil
}
func (s *slideSet) Slides() []*slide { return s.seqSlide }

func (s *slideSet) HasImage(zid api.ZettelID) bool {
	_, found := s.setImage[zid]
	return found
}
func (s *slideSet) AddImage(zid api.ZettelID, syntax string, data []byte) {
	s.setImage[zid] = image{syntax, data}
}
func (s *slideSet) GetImage(zid api.ZettelID) (image, bool) {
	img, found := s.setImage[zid]
	return img, found
}
func (s *slideSet) Images() []api.ZettelID {
	result := make([]api.ZettelID, 0, len(s.setImage))
	for zid := range s.setImage {
		result = append(result, zid)
	}
	return result
}

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

type getZettelContentFunc func(api.ZettelID) ([]byte, error)
type getZettelZSONFunc func(api.ZettelID) (zjson.Value, error)

func (s *slideSet) AddSlide(zid api.ZettelID, getZettel getZettelZSONFunc) {
	if sl, found := s.setSlide[zid]; found {
		s.seqSlide = append(s.seqSlide, sl)
		return
	}
	zjZettel, err := getZettel(zid)
	if err != nil {
		// TODO: add artificial slide with error message / data
		return
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
	s.seqSlide = append(s.seqSlide, sl)
	s.setSlide[zid] = sl
}

func (s *slideSet) AdditionalSlide(zid api.ZettelID, m zjson.Meta, content zjson.Array) {
	// TODO: if first, add slide with text "additional content"
	sl := &slide{
		zid:     zid,
		meta:    m,
		content: content,
	}
	s.seqSlide = append(s.seqSlide, sl)
	s.setSlide[zid] = sl
}

func (s *slideSet) Completion(getZettel getZettelContentFunc, getZettelZJSON getZettelZSONFunc) {
	if s.completed {
		return
	}
	v := collectVisitor{getZettel: getZettel, getZettelZJSON: getZettelZJSON, s: s}
	v.Collect()
	s.completed = true
}

type collectVisitor struct {
	getZettel      getZettelContentFunc
	getZettelZJSON getZettelZSONFunc
	s              *slideSet
	stack          []api.ZettelID
	visited        map[api.ZettelID]*slide
}

func (v *collectVisitor) Push(zid api.ZettelID) {
	v.stack = append(v.stack, zid)
}
func (v *collectVisitor) Collect() {
	slides := v.s.Slides()
	for i := len(slides) - 1; i >= 0; i-- {
		v.Push(slides[i].zid)
	}
	// log.Println("STAC", v.stack)
	v.visited = make(map[api.ZettelID]*slide, len(slides))
	for {
		l := len(v.stack)
		if l == 0 {
			break
		}
		zid := v.stack[l-1]
		v.stack = v.stack[0 : l-1]
		// log.Println("ZIDD", zid)
		if _, found := v.visited[zid]; found {
			continue
		}
		sl := v.s.GetSlide(zid)
		if sl == nil {
			panic(zid)
		}
		v.visited[zid] = sl
		zjson.WalkBlock(v, sl.Content(), 0)
	}
}

func (v *collectVisitor) BlockArray(a zjson.Array, pos int) zjson.CloseFunc  { return nil }
func (v *collectVisitor) InlineArray(a zjson.Array, pos int) zjson.CloseFunc { return nil }
func (v *collectVisitor) ItemArray(a zjson.Array, pos int) zjson.CloseFunc   { return nil }
func (v *collectVisitor) Unexpected(val zjson.Value, pos int, exp string)    {}
func (v *collectVisitor) BlockObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	return true, nil
}

func (v *collectVisitor) InlineObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	switch t {
	case zjson.TypeLink:
		if q := zjson.GetString(obj, zjson.NameString2); q != zjson.RefStateZettel {
			break
		}
		s := zjson.GetString(obj, zjson.NameString)
		zid := api.ZettelID(s)
		if zid.IsValid() {
			v.visitZettel(zid)
			break
		}
		log.Println("LINK", pos, s, obj)
	case zjson.TypeEmbed:
		s := zjson.GetString(obj, zjson.NameString)
		zid := api.ZettelID(s)
		if zid.IsValid() {
			v.visitImage(zid, zjson.GetString(obj, zjson.NameString2))
			break
		}
		log.Println("EMBE", pos, obj)
	}
	return true, nil
}

func (v *collectVisitor) visitZettel(zid api.ZettelID) {
	if _, found := v.visited[zid]; found || v.s.GetSlide(zid) != nil {
		log.Println("DUPZ", zid)
		return
	}
	// log.Println("ZETT", zid)
	zjZettel, err := v.getZettelZJSON(zid)
	if err != nil {
		log.Println("GETZ", err)
		// TODO: add artificial slide with error message / data
		return
	}
	slMeta, slContent := zjson.GetMetaContent(zjZettel)
	if slMeta == nil || slContent == nil {
		// TODO: Add artificial slide with error message
		return
	}
	if vis := slMeta.GetString(api.KeyVisibility); vis != api.ValueVisibilityPublic {
		log.Println("VISZ", zid, vis)
		return
	}
	v.s.AdditionalSlide(zid, slMeta, slContent)
	v.Push(zid)
}

func (v *collectVisitor) visitImage(zid api.ZettelID, syntax string) {
	if v.s.HasImage(zid) {
		log.Println("DUPI", zid)
		return
	}

	// TODO: check for valid visibility

	data, err := v.getZettel(zid)
	if err != nil {
		log.Println("GETI", err)
		// TODO: add artificial image with error message / zid
		return
	}
	v.s.AddImage(zid, syntax, data)
}

// Utility function to retrieve some slide/slideset metadata.

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
