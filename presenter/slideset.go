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
	"strconv"

	"codeberg.org/t73fde/sxpf"
	"zettelstore.de/c/api"
	"zettelstore.de/c/sexpr"
	"zettelstore.de/c/zjson"
)

// Constants for zettel metadata keys
const (
	KeyAuthor       = "author"
	KeySlideSetRole = "slideset-role" // Only for Presenter configuration
	KeySlideRole    = "slide-role"
	KeySlideTitle   = "slide-title"
	KeySubTitle     = "sub-title" // TODO: Could possibly move to ZS-Client
)

// Constants for some values
const (
	DefaultSlideSetRole = "slideset"
	SlideRoleHandout    = "handout" // TODO: Includes manual?
	SlideRoleShow       = "show"
	SyntaxMermaid       = "mermaid"
)

// Slide is one slide that is shown one or more times.
type slide struct {
	zid      api.ZettelID // The zettel identifier
	zTitle   zjson.Array
	title    *sxpf.Pair
	lang     string
	role     string
	zContent zjson.Array // Zettel / slide content
	content  sxpf.Value
}

func newSlide(zid api.ZettelID, zMeta zjson.Meta, zContent zjson.Array, sxMeta sexpr.Meta, sxContent sxpf.Value) *slide {
	return &slide{
		zid:      zid,
		zTitle:   zGetSlideTitle(zMeta),
		title:    getSlideTitleZid(sxMeta, zid),
		lang:     sxMeta.GetString(api.KeyLang),
		role:     sxMeta.GetString(KeySlideRole),
		zContent: zContent,
		content:  sxContent,
	}
}
func (sl *slide) MakeChild(zTitle, zContent zjson.Array) *slide {
	return &slide{
		zid:      sl.zid,
		zTitle:   zTitle,
		lang:     sl.lang,
		role:     sl.role,
		zContent: zContent,
	}
}

func (sl *slide) ZTitle() zjson.Array   { return sl.zTitle }
func (sl *slide) Lang() string          { return sl.lang }
func (sl *slide) ZContent() zjson.Array { return sl.zContent }

func (sl *slide) HasSlideRole(sr string) bool {
	if sr == "" {
		return true
	}
	s := sl.role
	if s == "" {
		return true
	}
	return s == sr
}

type slideInfo struct {
	prev     *slideInfo
	Slide    *slide
	Number   int // number in document
	SlideNo  int // number in slide show, if any
	oldest   *slideInfo
	youngest *slideInfo
	next     *slideInfo
}

func (si *slideInfo) Next() *slideInfo {
	if si == nil {
		return nil
	}
	return si.next
}
func (si *slideInfo) Child() *slideInfo {
	if si == nil {
		return nil
	}
	return si.oldest
}
func (si *slideInfo) LastChild() *slideInfo {
	if si == nil {
		return nil
	}
	return si.youngest
}

func (si *slideInfo) SplitChildren() {
	var oldest, youngest *slideInfo
	zTitle := si.Slide.ZTitle()
	var zContent zjson.Array
	for _, zbn := range si.Slide.zContent {
		zobj := zjson.MakeObject(zbn)
		zti, found := zobj[zjson.NameType]
		if !found {
			return
		}
		if zjson.MakeString(zti) != zjson.TypeHeading {
			zContent = append(zContent, zbn)
			continue
		}
		if zLevel, err := strconv.Atoi(zjson.GetNumber(zobj)); err != nil || zLevel > 1 {
			zContent = append(zContent, zbn)
			continue
		}
		zNextTitle := zjson.GetArray(zobj, zjson.NameInline)
		if len(zNextTitle) == 0 {
			zContent = append(zContent, zbn)
			continue
		}
		slInfo := &slideInfo{
			prev:  youngest,
			Slide: si.Slide.MakeChild(zTitle, zContent),
		}
		zContent = nil
		if oldest == nil {
			oldest = slInfo
		}
		if youngest != nil {
			youngest.next = slInfo
		}
		youngest = slInfo
		zTitle = zNextTitle
	}
	if oldest == nil {
		oldest = &slideInfo{Slide: si.Slide.MakeChild(zTitle, zContent)}
		youngest = oldest
	} else {
		slInfo := &slideInfo{
			prev:  youngest,
			Slide: si.Slide.MakeChild(zTitle, zContent),
		}
		if youngest != nil {
			youngest.next = slInfo
		}
		youngest = slInfo
	}
	si.oldest = oldest
	si.youngest = youngest
}

func (si *slideInfo) FindSlide(zid api.ZettelID) *slideInfo {
	if si == nil {
		return nil
	}

	// Search backward
	for res := si; res != nil; res = res.prev {
		if res.Slide.zid == zid {
			return res
		}
	}

	// Search forward
	for res := si.next; res != nil; res = res.next {
		if res.Slide.zid == zid {
			return res
		}
	}
	return nil
}

type image struct {
	syntax string
	data   []byte
}

// slideSet is the sequence of slides shown.
type slideSet struct {
	zid         api.ZettelID
	zMeta       zjson.Meta // Metadata of slideset
	sxMeta      sexpr.Meta // Metadata of slideset
	seqSlide    []*slide   // slide may occur more than once in seq, but should be stored only once
	setSlide    map[api.ZettelID]*slide
	setImage    map[api.ZettelID]image
	isCompleted bool
	hasMermaid  bool
}

func newSlideSet(zid api.ZettelID, zjMeta zjson.Value, sxMeta sexpr.Meta) *slideSet {
	zm := zjson.MakeMeta(zjMeta)
	if len(zm) == 0 || len(sxMeta) == 0 {
		return nil
	}
	return newSlideSetMeta(zid, zm, sxMeta)
}
func newSlideSetMeta(zid api.ZettelID, zm zjson.Meta, sxMeta sexpr.Meta) *slideSet {
	return &slideSet{
		zid:      zid,
		zMeta:    zm,
		sxMeta:   sxMeta,
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

func (s *slideSet) SlideZids() []api.ZettelID {
	result := make([]api.ZettelID, len(s.seqSlide))
	for i, sl := range s.seqSlide {
		result[i] = sl.zid
	}
	return result
}

func (s *slideSet) Slides(role string, offset int) *slideInfo {
	switch role {
	case SlideRoleShow:
		return s.slidesforShow(offset)
	case SlideRoleHandout:
		return s.slidesForHandout(offset)
	}
	panic(role)
}
func (s *slideSet) slidesforShow(offset int) *slideInfo {
	var first, prev *slideInfo
	slideNo := offset
	for _, sl := range s.seqSlide {
		if !sl.HasSlideRole(SlideRoleShow) {
			continue
		}
		si := &slideInfo{
			prev:  prev,
			Slide: sl,
		}
		if first == nil {
			first = si
		}
		if prev != nil {
			prev.next = si
		}
		si.SlideNo = slideNo
		si.Number = slideNo
		prev = si

		si.SplitChildren()
		main := si.Child()
		main.SlideNo = slideNo
		main.Number = slideNo
		for sub := main.Next(); sub != nil; sub = sub.Next() {
			slideNo++
			sub.SlideNo = slideNo
			sub.Number = slideNo
		}
		slideNo++
	}
	return first
}
func (s *slideSet) slidesForHandout(offset int) *slideInfo {
	var first, prev *slideInfo
	number, slideNo := offset, offset
	for _, sl := range s.seqSlide {
		si := &slideInfo{
			prev:  prev,
			Slide: sl,
		}
		if !sl.HasSlideRole(SlideRoleHandout) {
			if sl.HasSlideRole(SlideRoleShow) {
				s.addChildrenForHandout(si, &slideNo)
			}
			continue
		}
		if sl.HasSlideRole(SlideRoleShow) {
			si.SlideNo = slideNo
			s.addChildrenForHandout(si, &slideNo)
		}
		if first == nil {
			first = si
		}
		if prev != nil {
			prev.next = si
		}
		si.Number = number
		prev = si
		number++
	}
	return first
}
func (*slideSet) addChildrenForHandout(si *slideInfo, slideNo *int) {
	si.SplitChildren()
	main := si.Child()
	main.SlideNo = *slideNo
	for sub := main.Next(); sub != nil; sub = sub.Next() {
		*slideNo++
		sub.SlideNo = *slideNo
	}
	*slideNo++
}

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

func (s *slideSet) ZTitle() zjson.Array { return zGetSlideTitle(s.zMeta) }
func (s *slideSet) ZSubtitle() zjson.Array {
	if subTitle := s.zMeta.GetArray(KeySubTitle); len(subTitle) > 0 {
		return subTitle
	}
	return nil
}
func (s *slideSet) Title(smk sxpf.SymbolMaker) *sxpf.Pair { return getSlideTitle(s.sxMeta) }

func (s *slideSet) Lang() string { return s.zMeta.GetString(api.KeyLang) }
func (s *slideSet) Author(cfg *slidesConfig) string {
	if author := s.zMeta.GetString(KeyAuthor); author != "" {
		return author
	}
	return cfg.author
}
func (s *slideSet) Copyright() string { return s.zMeta.GetString(api.KeyCopyright) }
func (s *slideSet) License() string   { return s.zMeta.GetString(api.KeyLicense) }

type getZettelContentFunc func(api.ZettelID) ([]byte, error)
type zGetZettelFunc func(api.ZettelID) (zjson.Value, error)
type sGetZettelFunc func(api.ZettelID) (sxpf.Value, error)

func (s *slideSet) AddSlide(zid api.ZettelID, zGetZettel zGetZettelFunc, sGetZettel sGetZettelFunc) {
	if sl, found := s.setSlide[zid]; found {
		s.seqSlide = append(s.seqSlide, sl)
		return
	}
	zjZettel, err := zGetZettel(zid)
	if err != nil {
		// TODO: add artificial slide with error message / data
		return
	}
	zslMeta, zslContent := zjson.GetMetaContent(zjZettel)
	if zslMeta == nil || zslContent == nil {
		// TODO: Add artificial slide with error message
		return
	}
	sxZettel, err := sGetZettel(zid)
	if err != nil {
		// TODO: add artificial slide with error message / data
		return
	}
	sxMeta, sxContent := sexpr.GetMetaContent(sxZettel)
	if sxMeta == nil || sxContent == nil {
		// TODO: Add artificial slide with error message
		return
	}
	sl := newSlide(zid, zslMeta, zslContent, sxMeta, sxContent)
	s.seqSlide = append(s.seqSlide, sl)
	s.setSlide[zid] = sl
}

func (s *slideSet) AdditionalSlide(zid api.ZettelID, zm zjson.Meta, zContent zjson.Array, sxMeta sexpr.Meta, sxContent sxpf.Value) {
	// TODO: if first, add slide with text "additional content"
	sl := newSlide(zid, zm, zContent, sxMeta, sxContent)
	s.seqSlide = append(s.seqSlide, sl)
	s.setSlide[zid] = sl
}

func (s *slideSet) Completion(getZettel getZettelContentFunc, getZettelZJSON zGetZettelFunc, getZettelSexpr sGetZettelFunc) {
	if s.isCompleted {
		return
	}
	v := collectVisitor{getZettel: getZettel, zGetZettel: getZettelZJSON, sGetZettel: getZettelSexpr, s: s}
	zids := s.SlideZids()
	for i := len(zids) - 1; i >= 0; i-- {
		v.Push(zids[i])
	}
	// log.Println("STAC", v.stack)
	v.visited = make(map[api.ZettelID]*slide, len(zids)+16)
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
		sl := s.GetSlide(zid)
		if sl == nil {
			panic(zid)
		}
		v.visited[zid] = sl

		zjson.WalkBlock(&v, sl.ZContent(), 0)
	}

	s.isCompleted = true
}

type collectState struct {
	stack   []api.ZettelID
	visited map[api.ZettelID]*slide
}

func newCollectState(s *slideSet) collectState {
	var result collectState
	zids := s.SlideZids()
	for i := len(zids) - 1; i >= 0; i-- {
		result.push(zids[i])
	}
	// log.Println("STAC", result.stack)
	result.visited = make(map[api.ZettelID]*slide, len(zids)+16)
	return result
}
func (cs *collectState) push(zid api.ZettelID) { cs.stack = append(cs.stack, zid) }

type collectVisitor struct {
	getZettel  getZettelContentFunc
	zGetZettel zGetZettelFunc
	sGetZettel sGetZettelFunc
	s          *slideSet
	stack      []api.ZettelID
	visited    map[api.ZettelID]*slide
}

func (v *collectVisitor) Push(zid api.ZettelID) { v.stack = append(v.stack, zid) }

func (v *collectVisitor) BlockArray(a zjson.Array, pos int) zjson.CloseFunc  { return nil }
func (v *collectVisitor) InlineArray(a zjson.Array, pos int) zjson.CloseFunc { return nil }
func (v *collectVisitor) ItemArray(a zjson.Array, pos int) zjson.CloseFunc   { return nil }
func (v *collectVisitor) Unexpected(val zjson.Value, pos int, exp string)    {}
func (v *collectVisitor) BlockObject(t string, obj zjson.Object, pos int) (bool, zjson.CloseFunc) {
	if t == zjson.TypeVerbatimEval {
		if syntax, found := zjson.GetAttributes(obj).Get(""); found && syntax == SyntaxMermaid {
			v.s.hasMermaid = true
		}
	}
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
		return
	}
	// log.Println("ZETT", zid)
	zjZettel, err := v.zGetZettel(zid)
	if err != nil {
		log.Println("GETZ", err)
		// TODO: add artificial slide with error message / data
		return
	}
	slMeta, slContent := zjson.GetMetaContent(zjZettel)
	if slMeta == nil || slContent == nil {
		// TODO: Add artificial slide with error message
		log.Println("MECO", zid)
		return
	}
	sxZettel, err := v.sGetZettel(zid)
	if err != nil {
		log.Println("GETS", err)
		// TODO: add artificial slide with error message / data
		return
	}
	sxMeta, sxContent := sexpr.GetMetaContent(sxZettel)
	if sxMeta == nil || sxContent == nil {
		// TODO: Add artificial slide with error message
		log.Println("MECo", zid)
		return
	}

	if vis := sxMeta.GetString(api.KeyVisibility); vis != api.ValueVisibilityPublic {
		// log.Println("VISZ", zid, vis)
		return
	}
	v.s.AdditionalSlide(zid, slMeta, slContent, sxMeta, sxContent)
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

func zGetSlideTitle(zm zjson.Meta) zjson.Array {
	if zTitle := zm.GetArray(KeySlideTitle); len(zTitle) > 0 {
		return zTitle
	}
	return zm.GetArray(api.KeyTitle)
}
func zGetZettelTitleZid(zm zjson.Meta, zid api.ZettelID) zjson.Array {
	if zTitle := zm.GetArray(api.KeyTitle); len(zTitle) > 0 {
		return zTitle
	}
	return zjson.Array{zjson.Object{zjson.NameType: zjson.TypeText, zjson.NameString: string(zid)}}
}

func getSlideTitle(sxMeta sexpr.Meta) *sxpf.Pair {
	if title := sxMeta.GetPair(KeySlideTitle); title != nil && !title.IsEmpty() {
		return title
	}
	return sxMeta.GetPair(api.KeyTitle)
}
func getSlideTitleZid(sxMeta sexpr.Meta, zid api.ZettelID) *sxpf.Pair {
	if title := getSlideTitle(sxMeta); title != nil && !title.IsEmpty() {
		return title
	}
	return sxpf.NewPair(sxpf.NewPair(sexpr.SymText, sxpf.NewPair(sxpf.NewString(string(zid)), nil)), nil)
}
