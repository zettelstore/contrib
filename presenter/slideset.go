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
	"log"

	"codeberg.org/t73fde/sxpf"
	"zettelstore.de/c/api"
	"zettelstore.de/c/sz"
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
	zid     api.ZettelID // The zettel identifier
	title   *sxpf.List
	lang    string
	role    string
	content *sxpf.List // Zettel / slide content
}

func newSlide(zid api.ZettelID, sxMeta sz.Meta, sxContent *sxpf.List, zs *sz.ZettelSymbols) *slide {
	return &slide{
		zid:     zid,
		title:   getSlideTitleZid(sxMeta, zid, zs),
		lang:    sxMeta.GetString(api.KeyLang),
		role:    sxMeta.GetString(KeySlideRole),
		content: sxContent,
	}
}
func (sl *slide) MakeChild(sxTitle, sxContent *sxpf.List) *slide {
	return &slide{
		zid:     sl.zid,
		title:   sxTitle,
		lang:    sl.lang,
		role:    sl.role,
		content: sxContent,
	}
}

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

func (si *slideInfo) SplitChildren(zs *sz.ZettelSymbols) {
	var oldest, youngest *slideInfo
	title := si.Slide.title
	var content []sxpf.Object
	// First element of si.Slide.content is the BLOCK symbol. Ignore it.
	for elem := si.Slide.content.Tail(); !elem.IsNil(); elem = elem.Tail() {
		bn, ok := sxpf.GetList(elem.Car())
		if !ok || bn == nil {
			break
		}
		sym, ok := sxpf.GetSymbol(bn.Car())
		if !ok {
			break
		}
		if !sym.IsEqual(zs.SymHeading) {
			content = append(content, bn)
			continue
		}
		levelPair := bn.Tail()
		num, ok := sxpf.GetNumber(levelPair.Car())
		if !ok {
			break
		}
		if level := num.(sxpf.Int64); level != 1 {
			content = append(content, bn)
			continue
		}

		nextTitle := levelPair.Tail().Tail().Tail().Tail().Head()
		if nextTitle == nil {
			content = append(content, bn)
			continue
		}
		slInfo := &slideInfo{
			prev:  youngest,
			Slide: si.Slide.MakeChild(title, sxpf.MakeList(content...)),
		}
		content = nil
		if oldest == nil {
			oldest = slInfo
		}
		if youngest != nil {
			youngest.next = slInfo
		}
		youngest = slInfo
		title = nextTitle
	}
	if oldest == nil {
		oldest = &slideInfo{Slide: si.Slide.MakeChild(title, sxpf.MakeList(content...))}
		youngest = oldest
	} else {
		slInfo := &slideInfo{
			prev:  youngest,
			Slide: si.Slide.MakeChild(title, sxpf.MakeList(content...)),
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
	sxMeta      sz.Meta  // Metadata of slideset
	seqSlide    []*slide // slide may occur more than once in seq, but should be stored only once
	setSlide    map[api.ZettelID]*slide
	setImage    map[api.ZettelID]image
	isCompleted bool
	hasMermaid  bool
	zs          *sz.ZettelSymbols
}

func newSlideSet(zid api.ZettelID, sxMeta sz.Meta, zs *sz.ZettelSymbols) *slideSet {
	if len(sxMeta) == 0 {
		return nil
	}
	return newSlideSetMeta(zid, sxMeta, zs)
}
func newSlideSetMeta(zid api.ZettelID, sxMeta sz.Meta, zs *sz.ZettelSymbols) *slideSet {
	return &slideSet{
		zid:      zid,
		sxMeta:   sxMeta,
		setSlide: make(map[api.ZettelID]*slide),
		setImage: make(map[api.ZettelID]image),
		zs:       zs,
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
			prev:    prev,
			Slide:   sl,
			SlideNo: slideNo,
			Number:  slideNo,
		}
		if first == nil {
			first = si
		}
		if prev != nil {
			prev.next = si
		}
		prev = si

		si.SplitChildren(s.zs)
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
func (s *slideSet) addChildrenForHandout(si *slideInfo, slideNo *int) {
	si.SplitChildren(s.zs)
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

func (s *slideSet) Title(zs *sz.ZettelSymbols) *sxpf.List { return getSlideTitle(s.sxMeta, zs) }
func (s *slideSet) Subtitle() *sxpf.List                  { return s.sxMeta.GetList(KeySubTitle) }

func (s *slideSet) Lang() string { return s.sxMeta.GetString(api.KeyLang) }
func (s *slideSet) Author(cfg *slidesConfig) string {
	if author := s.sxMeta.GetString(KeyAuthor); author != "" {
		return author
	}
	return cfg.author
}
func (s *slideSet) Copyright() string { return s.sxMeta.GetString(api.KeyCopyright) }
func (s *slideSet) License() string   { return s.sxMeta.GetString(api.KeyLicense) }

type getZettelContentFunc func(api.ZettelID) ([]byte, error)
type sGetZettelFunc func(api.ZettelID) (sxpf.Object, error)

func (s *slideSet) AddSlide(zid api.ZettelID, sGetZettel sGetZettelFunc, zs *sz.ZettelSymbols) {
	if sl, found := s.setSlide[zid]; found {
		s.seqSlide = append(s.seqSlide, sl)
		return
	}

	sxZettel, err := sGetZettel(zid)
	if err != nil {
		// TODO: add artificial slide with error message / data
		return
	}
	sxMeta, sxContent := sz.GetMetaContent(sxZettel)
	if sxMeta == nil || sxContent == nil {
		// TODO: Add artificial slide with error message
		return
	}
	sl := newSlide(zid, sxMeta, sxContent, zs)
	s.seqSlide = append(s.seqSlide, sl)
	s.setSlide[zid] = sl
}

func (s *slideSet) AdditionalSlide(zid api.ZettelID, sxMeta sz.Meta, sxContent *sxpf.List, zs *sz.ZettelSymbols) {
	// TODO: if first, add slide with text "additional content"
	sl := newSlide(zid, sxMeta, sxContent, zs)
	s.seqSlide = append(s.seqSlide, sl)
	s.setSlide[zid] = sl
}

func (s *slideSet) Completion(getZettel getZettelContentFunc, getZettelSexpr sGetZettelFunc, zs *sz.ZettelSymbols) {
	if s.isCompleted {
		return
	}
	env := collectEnv{zs: zs, s: s, getZettel: getZettel, sGetZettel: getZettelSexpr}
	env.initCollection(s)
	for {
		zid, found := env.pop()
		if !found {
			break
		}
		if zid == api.InvalidZID {
			continue
		}
		sl := s.GetSlide(zid)
		if sl == nil {
			panic(zid)
		}
		env.mark(zid)
		env.visitContent(sl.content)
	}
	s.hasMermaid = env.hasMermaid
	s.isCompleted = true
}

func (ce *collectEnv) initCollection(s *slideSet) {
	zids := s.SlideZids()
	for i := len(zids) - 1; i >= 0; i-- {
		ce.push(zids[i])
	}
	ce.visited = make(map[api.ZettelID]struct{}, len(zids)+16)
}
func (ce *collectEnv) push(zid api.ZettelID) { ce.stack = append(ce.stack, zid) }
func (ce *collectEnv) pop() (api.ZettelID, bool) {
	lp := len(ce.stack) - 1
	if lp < 0 {
		return api.InvalidZID, false
	}
	zid := ce.stack[lp]
	ce.stack = ce.stack[0:lp]
	if _, found := ce.visited[zid]; found {
		return api.InvalidZID, true
	}
	return zid, true
}
func (ce *collectEnv) mark(zid api.ZettelID) { ce.visited[zid] = struct{}{} }
func (ce *collectEnv) isMarked(zid api.ZettelID) bool {
	_, found := ce.visited[zid]
	return found
}

type collectEnv struct {
	zs         *sz.ZettelSymbols
	s          *slideSet
	getZettel  getZettelContentFunc
	sGetZettel sGetZettelFunc
	stack      []api.ZettelID
	visited    map[api.ZettelID]struct{}
	hasMermaid bool
}

func (ce *collectEnv) visitContent(content *sxpf.List) {
	if content == nil {
		return
	}
	for elem := content.Tail(); elem != nil; elem = elem.Tail() {
		switch o := elem.Car().(type) {
		case *sxpf.List:
			sym, ok := sxpf.GetSymbol(o.Car())
			if !ok {
				continue
			}
			zs := ce.zs
			if zs.SymText.IsEql(sym) || zs.SymSpace.IsEql(sym) {
				continue
			}
			if zs.SymVerbatimEval.IsEql(sym) {
				if hasMermaidAttribute(o.Tail()) {
					ce.hasMermaid = true
				}
			} else if zs.SymLinkZettel.IsEql(sym) {
				if zidVal, isString := sxpf.GetString(o.Tail().Tail().Car()); isString {
					if zid := api.ZettelID(zidVal); zid.IsValid() {
						ce.visitZettel(zid)
					}
				}
			} else if zs.SymEmbed.IsEql(sym) {
				argRef := o.Tail().Tail()
				qref, isList := sxpf.GetList(argRef.Car())
				if !isList {
					continue
				}
				ref, isList := sxpf.GetList(qref.Tail().Car())
				if !isList {
					continue
				}
				symEmbedRefState, isSymbol := sxpf.GetSymbol(ref.Car())
				if !isSymbol || !zs.SymRefStateZettel.IsEql(symEmbedRefState) {
					continue
				}
				zidVal, isString := sxpf.GetString(ref.Tail().Car())
				if !isString {
					continue
				}
				zid := api.ZettelID(zidVal)
				if !zid.IsValid() {
					continue
				}
				syntax, isString := sxpf.GetString(argRef.Tail().Car())
				if !isString {
					continue
				}
				ce.visitImage(zid, syntax.String())
			} else {
				ce.visitContent(o)
			}
		case sxpf.Number:
		case sxpf.String:
		default:
			log.Printf("ELEM %T/%v", o, o)
		}
	}
}

func hasMermaidAttribute(args *sxpf.List) bool {
	lst, ok := sxpf.GetList(args.Car())
	if !ok {
		return false
	}
	attr, ok := sxpf.GetList(lst.Tail().Car())
	if !ok {
		return false
	}
	a := sz.GetAttributes(attr)
	if syntax, found := a.Get(""); found && syntax == SyntaxMermaid {
		return true
	}
	return false
}

func (ce *collectEnv) visitZettel(zid api.ZettelID) {
	if ce.isMarked(zid) || ce.s.GetSlide(zid) != nil {
		return
	}
	sxZettel, err := ce.sGetZettel(zid)
	if err != nil {
		log.Println("GETS", err)
		// TODO: add artificial slide with error message / data
		return
	}
	sxMeta, sxContent := sz.GetMetaContent(sxZettel)
	if sxMeta == nil || sxContent == nil {
		// TODO: Add artificial slide with error message
		log.Println("MECo", zid)
		return
	}

	if vis := sxMeta.GetString(api.KeyVisibility); vis != api.ValueVisibilityPublic {
		// log.Println("VISZ", zid, vis)
		return
	}
	ce.s.AdditionalSlide(zid, sxMeta, sxContent, ce.zs)
	ce.push(zid)
}

func (ce *collectEnv) visitImage(zid api.ZettelID, syntax string) {
	if ce.s.HasImage(zid) {
		log.Println("DUPI", zid)
		return
	}

	// TODO: check for valid visibility

	data, err := ce.getZettel(zid)
	if err != nil {
		log.Println("GETI", err)
		// TODO: add artificial image with error message / zid
		return
	}
	ce.s.AddImage(zid, syntax, data)
}

// Utility function to retrieve some slide/slideset metadata.

func getZettelTitleZid(sxMeta sz.Meta, zid api.ZettelID, zs *sz.ZettelSymbols) *sxpf.List {
	if title := sxMeta.GetList(api.KeyTitle); title != nil {
		return title
	}
	return sxpf.Cons(zs.SymText, sxpf.Cons(sxpf.MakeString(string(zid)), sxpf.Nil()))
}

func getSlideTitle(sxMeta sz.Meta, zs *sz.ZettelSymbols) *sxpf.List {
	if title := sxMeta.GetList(KeySlideTitle); title != nil {
		return title
	}
	if title := sxMeta.GetString(KeySlideTitle); title != "" {
		return makeTitleList(title, zs)
	}
	if title := sxMeta.GetList(api.KeyTitle); title != nil {
		return title
	}
	if title := sxMeta.GetString(api.KeyTitle); title != "" {
		return makeTitleList(title, zs)
	}
	return nil
}

func getSlideTitleZid(sxMeta sz.Meta, zid api.ZettelID, zs *sz.ZettelSymbols) *sxpf.List {
	if title := getSlideTitle(sxMeta, zs); title != nil {
		return title
	}
	return makeTitleList(string(zid), zs)
}

func makeTitleList(s string, zs *sz.ZettelSymbols) *sxpf.List {
	return sxpf.MakeList(zs.SymInline, sxpf.MakeList(zs.SymText, sxpf.MakeString(s)))
}
