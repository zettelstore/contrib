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
	content  *sxpf.Pair
}

func newSlide(zid api.ZettelID, zMeta zjson.Meta, zContent zjson.Array, sxMeta sexpr.Meta, sxContent *sxpf.Pair) *slide {
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
func (sl *slide) MakeChild(zTitle, zContent zjson.Array, sxContent *sxpf.Pair) *slide {
	return &slide{
		zid:      sl.zid,
		zTitle:   zTitle,
		lang:     sl.lang,
		role:     sl.role,
		zContent: zContent,
		content:  sxContent,
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

func (si *slideInfo) SplitChildren() {
	var oldest, youngest *slideInfo
	zTitle := si.Slide.zTitle
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
			Slide: si.Slide.MakeChild(zTitle, zContent, nil),
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
		oldest = &slideInfo{Slide: si.Slide.MakeChild(zTitle, zContent, nil)}
		youngest = oldest
	} else {
		slInfo := &slideInfo{
			prev:  youngest,
			Slide: si.Slide.MakeChild(zTitle, zContent, nil),
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
func (s *slideSet) Title() *sxpf.Pair { return getSlideTitle(s.sxMeta) }
func (s *slideSet) Subtitle() *sxpf.Pair {
	if subTitle := s.sxMeta.GetPair(KeySubTitle); !subTitle.IsEmpty() {
		return subTitle
	}
	return nil
}

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

func (s *slideSet) AdditionalSlide(zid api.ZettelID, zm zjson.Meta, zContent zjson.Array, sxMeta sexpr.Meta, sxContent *sxpf.Pair) {
	// TODO: if first, add slide with text "additional content"
	sl := newSlide(zid, zm, zContent, sxMeta, sxContent)
	s.seqSlide = append(s.seqSlide, sl)
	s.setSlide[zid] = sl
}

func (s *slideSet) Completion(getZettel getZettelContentFunc, getZettelZJSON zGetZettelFunc, getZettelSexpr sGetZettelFunc) {
	if s.isCompleted {
		return
	}
	env := collectEnv{s: s, getZettel: getZettel, zGetZettel: getZettelZJSON, sGetZettel: getZettelSexpr}
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
		sxpf.Eval(&env, sl.content)
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
	s          *slideSet
	getZettel  getZettelContentFunc
	zGetZettel zGetZettelFunc
	sGetZettel sGetZettelFunc
	stack      []api.ZettelID
	visited    map[api.ZettelID]struct{}
	hasMermaid bool
}

func (ce *collectEnv) LookupForm(sym *sxpf.Symbol) (sxpf.Form, error) {
	switch sym {
	case sexpr.SymVerbatimEval:
		return verbEvalFn, nil
	case sexpr.SymLinkZettel:
		return linkZettelFn, nil
	case sexpr.SymEmbed:
		return embedFn, nil
	}
	return ignoreFn, nil
}

var (
	verbEvalFn = sxpf.NewBuiltin("verbatim-eval", true, 1, -1,
		func(env sxpf.Environment, args *sxpf.Pair, _ int) (sxpf.Value, error) {
			if hasMermaidAttribute(args) {
				env.(*collectEnv).hasMermaid = true
			}
			return nil, nil
		})
	linkZettelFn = sxpf.NewBuiltin("link-zettel", true, 2, -1,
		func(env sxpf.Environment, args *sxpf.Pair, _ int) (sxpf.Value, error) {
			if zidVal, err := args.GetTail().GetString(); err == nil {
				zid := api.ZettelID(zidVal)
				if zid.IsValid() {
					env.(*collectEnv).visitZettel(zid)
				}
			}
			return nil, nil
		})
	embedFn = sxpf.NewBuiltin("embed-inline", true, 3, -1,
		func(env sxpf.Environment, args *sxpf.Pair, _ int) (sxpf.Value, error) {
			argRef := args.GetTail()
			if ref, err := argRef.GetPair(); err == nil && ref.GetFirst() == sexpr.SymRefStateZettel {
				if zidVal, ok := ref.GetTail().GetString(); ok == nil {
					zid := api.ZettelID(zidVal)
					if syntax, err := argRef.GetTail().GetString(); err == nil && zid.IsValid() {
						env.(*collectEnv).visitImage(zid, syntax)
					}
				}
			}
			return nil, nil
		})
	ignoreFn = sxpf.NewBuiltin("traverse", false, 0, -1,
		func(sxpf.Environment, *sxpf.Pair, int) (sxpf.Value, error) { return nil, nil })
)

func hasMermaidAttribute(args *sxpf.Pair) bool {
	if p, ok := args.GetFirst().(*sxpf.Pair); ok {
		if syntax, found := sexpr.GetAttributes(p).Get(""); found && syntax == SyntaxMermaid {
			return true
		}
	}
	return false
}

func (ce *collectEnv) EvalPair(p *sxpf.Pair) (sxpf.Value, error)       { return sxpf.EvalCallOrSeq(ce, p) }
func (ce *collectEnv) EvalSymbol(sym *sxpf.Symbol) (sxpf.Value, error) { return sym, nil }
func (ce *collectEnv) EvalOther(val sxpf.Value) (sxpf.Value, error)    { return val, nil }

func (ce *collectEnv) visitZettel(zid api.ZettelID) {
	if ce.isMarked(zid) || ce.s.GetSlide(zid) != nil {
		return
	}
	zjZettel, err := ce.zGetZettel(zid)
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
	sxZettel, err := ce.sGetZettel(zid)
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
	ce.s.AdditionalSlide(zid, slMeta, slContent, sxMeta, sxContent)
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
