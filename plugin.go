// Package groups is a plugin that organises entities into a tree of groups the assembly builds, with a page to walk it: the groups
// under the one on show, the entities in it, and then those in the groups below it. Each group is an entity too.
package groups

import (
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/avanha/pmaas-plugin-groups/data"
	"github.com/avanha/pmaas-plugin-groups/internal/http"
	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/device"
	"github.com/avanha/pmaas-spi/entity"
	"github.com/avanha/pmaas-spi/group"
	"github.com/avanha/pmaas-spi/logging"
)

type Plugin interface {
	spi.IPMAASPlugin
}

// plugin keeps nothing that changes: the tree is fixed, and what is in it is looked up each time it is asked about, because the
// entities appear as their plugins start and go as they stop.
type plugin struct {
	log       *slog.Logger
	config    PluginConfig
	container spi.IPMAASContainer
	http      *http.Handler

	// registered are the ids of the groups' own entities, by group, kept to deregister them.
	registered []string
}

var _ spi.IPMAASPlugin = (*plugin)(nil)

func NewPlugin(config PluginConfig) Plugin {
	if config.tree == nil {
		panic("groups: the config must come from NewPluginConfig")
	}

	if config.Name == "" {
		config.Name = "Groups"
	}

	return &plugin{config: config, http: http.NewHandler()}
}

func (p *plugin) ShortName() string {
	return "groups"
}

func (p *plugin) Init(container spi.IPMAASContainer) {
	p.container = container
	p.log = logging.For(container)

	lookup := p.lookupByName
	for _, named := range p.config.tree.named {
		named.lookup.Store(&lookup)
	}

	p.http.Init(container, p, p.config.Name)
}

func (p *plugin) Start() {
	p.log.Info("starting")

	p.config.tree.root.walk(func(g *Group) {
		stub := &groupStub{p: p, g: g}

		id, err := p.container.RegisterEntity("group/"+g.Path(), group.IGroupType, g.Name(), func() (any, error) { return stub, nil })
		if err != nil {
			p.log.Error("unable to register a group", "path", g.Path(), "error", err)

			return
		}

		p.registered = append(p.registered, id)
	})

	p.log.Info("started", "groups", len(p.registered))
}

func (p *plugin) Stop() chan func() {
	p.log.Info("stopping")

	for _, id := range p.registered {
		if err := p.container.DeregisterEntity(id); err != nil {
			p.log.Warn("unable to deregister a group", "id", id, "error", err)
		}
	}

	p.registered = nil

	return p.container.ClosedCallbackChannel()
}

// registered returns what the server has registered.
func (p *plugin) entities() []entity.RegisteredEntityInfo {
	all, err := p.container.GetEntities(func(*entity.RegisteredEntityInfo) bool { return true })
	if err != nil {
		p.log.Error("unable to list the entities", "error", err)

		return nil
	}

	return all
}

// lookupByName is the id of the one entity with the name.
func (p *plugin) lookupByName(name string) (string, bool) {
	var found string

	for _, info := range p.entities() {
		if info.Name != name {
			continue
		}

		if found != "" {
			p.log.Warn("more than one entity has the name, so it is not used", "name", name)

			return "", false
		}

		found = info.Id
	}

	return found, found != ""
}

// claimed is who is in which group right now: refs resolved, and each entity given to the first group that claims it.
type claimed struct {
	// members are the entities in each group directly, in the order they were added, each once across the tree.
	members map[*Group][]claimedMember

	// ids is every entity in any group, hidden or not.
	ids map[string]bool

	// unresolved are the refs that name no entity yet, by group.
	unresolved map[*Group][]entity.Ref
}

type claimedMember struct {
	id     string
	hidden bool
}

func (p *plugin) claim() claimed {
	c := claimed{members: map[*Group][]claimedMember{}, ids: map[string]bool{}, unresolved: map[*Group][]entity.Ref{}}

	p.config.tree.root.walk(func(g *Group) {
		for _, m := range g.members {
			id, ok := m.ref.RegisteredId()

			switch {
			case !ok:
				c.unresolved[g] = append(c.unresolved[g], m.ref)
			case c.ids[id]:
				p.log.Warn("an entity is in more than one group, so it stays in the first", "entity", id, "group", g.Path())
			default:
				c.ids[id] = true
				c.members[g] = append(c.members[g], claimedMember{id: id, hidden: m.hidden})
			}
		}
	})

	return c
}

// shown are the ids of a group's own members that its page shows.
func (c claimed) shown(g *Group) []string {
	var ids []string

	for _, m := range c.members[g] {
		if !m.hidden {
			ids = append(ids, m.id)
		}
	}

	return ids
}

// subtree are the groups below the group, depth first, not including it.
func subtree(g *Group) []*Group {
	var below []*Group

	for _, child := range g.children {
		child.walk(func(d *Group) { below = append(below, d) })
	}

	return below
}

// countShown is how many entities the group's page shows below the heading: its own and those of the groups under it.
func (c claimed) countShown(g *Group) int {
	n := len(c.shown(g))

	for _, d := range subtree(g) {
		n += len(c.shown(d))
	}

	return n
}

// href is where the group's page is.
func (p *plugin) href(g *Group) string {
	base := p.container.RouteFullPath("")
	if g.parent == nil {
		return base
	}

	return base + "?g=" + url.QueryEscape(g.Path())
}

// Page makes the page of the group at the path (see data.Page). It reports false if there is none.
func (p *plugin) Page(path string) (data.Page, bool) {
	g := p.config.tree.root.find(path)
	if g == nil {
		return data.Page{}, false
	}

	c := p.claim()
	page := data.Page{Title: g.Name(), Header: data.Header{Crumbs: p.crumbs(g)}}

	for i, child := range g.children {
		page.Header.Children = append(page.Header.Children,
			data.ChildGroup{Name: child.Name(), Href: p.href(child), Groups: len(child.children), Entities: c.countShown(child), Hue: tileHue(i)})
	}

	// The entities of the group itself, and those of the groups below it, which come after.
	var below []string
	for _, d := range subtree(g) {
		below = append(below, c.shown(d)...)
	}

	direct := c.shown(g)

	var ungrouped []string

	if g.parent == nil && p.config.ShowUngrouped {
		ungrouped = p.ungrouped(c)
	}

	ids := append(append(append([]string{}, direct...), below...), ungrouped...)
	results := make(map[string]spi.EntityViewResult, len(ids))

	for _, result := range p.container.GetEntityViews(ids) {
		results[result.EntityId] = result
	}

	seen := map[string]bool{}

	draw := func(id string, explicit bool) (entry, bool) {
		e, key, ok := p.entry(results[id], explicit)
		if !ok || seen[key] {
			return entry{}, false
		}

		seen[key] = true

		return e, true
	}

	// The groups' entities are drawn first, so that a card they own is theirs: a device whose fan is in a group and whose light is in
	// none is one card, and it belongs to the group. What is in no group is drawn last, and is listed with the group's own.
	var directEntries, belowEntries []entry

	for _, id := range direct {
		if e, ok := draw(id, true); ok {
			directEntries = append(directEntries, e)
		}
	}

	for _, id := range below {
		if e, ok := draw(id, true); ok {
			belowEntries = append(belowEntries, e)
		}
	}

	for _, id := range ungrouped {
		if e, ok := draw(id, false); ok {
			directEntries = append(directEntries, e)
		}
	}

	page.Items = append(page.Items, p.byCategory(directEntries)...)

	if len(belowEntries) > 0 {
		if len(directEntries) > 0 {
			page.Items = append(page.Items, data.Divider{Text: "In the groups below", Major: true, Hue: belowHue})
		}

		page.Items = append(page.Items, p.byCategory(belowEntries)...)
	}

	page.Header.Notes = p.notes(c, g)
	page.Header.Empty = len(page.Items) == 0 && len(page.Header.Children) == 0

	return page, true
}

// entry is an entity as the page lists it: what is drawn, and what it is sorted by.
type entry struct {
	id       string
	name     string
	category string
	item     any
}

// entry is what the page lists for an entity: its view if its plugin gave one, or else a summary of it, if it was asked for by name.
// The key is the card's: entities that share one are drawn once.
func (p *plugin) entry(r spi.EntityViewResult, explicit bool) (e entry, key string, ok bool) {
	if r.EntityId == "" {
		return entry{}, "", false
	}

	if r.View != nil && r.Err == nil {
		key = r.View.Key
		if key == "" {
			key = "entity:" + r.EntityId
		}

		category := r.View.Category
		if category == "" {
			category = categoryOfType(r.EntityType)
		}

		return entry{id: r.EntityId, name: r.Name, category: category, item: r.View.Value}, key, true
	}

	if !explicit && r.Err == nil {
		return entry{}, "", false
	}

	summary := data.Summary{Name: r.Name, Plugin: r.Plugin, PluginHref: r.PluginPath}
	if r.EntityType != nil {
		summary.Type = typeName(r.EntityType)
	}

	if r.Err != nil {
		summary.Error = r.Err.Error()
	}

	return entry{id: r.EntityId, name: r.Name, category: categoryOfType(r.EntityType), item: summary}, "entity:" + r.EntityId, true
}

// categoryOfType is the kind of an entity from the type it was registered with, for a view that doesn't say.
func categoryOfType(t reflect.Type) string {
	if t == nil {
		return group.CategoryOther
	}

	// Most specific first: a fan and a light are switches as well.
	for _, k := range []struct {
		kind     reflect.Type
		category string
	}{
		{device.IFanType, group.CategoryFans},
		{device.ILightType, group.CategoryLights},
		{device.IPowerMonitorType, group.CategoryPower},
		{device.IDoorSensorType, group.CategorySensors},
		{device.IButtonType, group.CategoryButtons},
		{device.ISwitchType, group.CategorySwitches},
	} {
		if t.AssignableTo(k.kind) {
			return k.category
		}
	}

	return group.CategoryOther
}

// belowHue is the colour of the heading for the groups below: a slate blue, set apart from the kinds.
const belowHue = 215

// categoryHues are the colours of the kinds the plugin knows. Any other is given one from its name, so it is always the same.
var categoryHues = map[string]int{
	group.CategoryFans:         175,
	group.CategoryLights:       40,
	group.CategorySwitches:     205,
	group.CategoryThermometers: 12,
	group.CategorySensors:      270,
	group.CategoryPower:        135,
	group.CategoryButtons:      325,
	group.CategoryOther:        220,
}

func categoryHue(category string) int {
	if hue, ok := categoryHues[category]; ok {
		return hue
	}

	return hueOf(category)
}

// tileHue is the colour of the i'th group tile. The hues are a golden angle apart, so neighbours are as different as can be however
// many there are, and a group keeps its colour for as long as the groups before it are the same.
func tileHue(i int) int {
	return (25 + i*137) % 360
}

// hueOf is a colour, 0 to 359, worked out from the text, so that the same text always has the same one.
func hueOf(text string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(text))

	return int(hash.Sum32() % 360)
}

// byCategory is the entries as items, grouped by kind, each kind under a divider, in the configured order of kinds and then by name.
func (p *plugin) byCategory(entries []entry) []any {
	order := p.config.CategoryOrder
	if len(order) == 0 {
		order = group.Categories
	}

	rank := func(category string) int {
		if category == group.CategoryOther {
			return len(order) + 1
		}

		for i, name := range order {
			if name == category {
				return i
			}
		}

		return len(order)
	}

	sorted := append([]entry{}, entries...)

	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]

		if ra, rb := rank(a.category), rank(b.category); ra != rb {
			return ra < rb
		}

		if a.category != b.category {
			return a.category < b.category
		}

		if an, bn := strings.ToLower(a.name), strings.ToLower(b.name); an != bn {
			return an < bn
		}

		return a.id < b.id
	})

	var items []any

	for i, e := range sorted {
		if i == 0 || sorted[i-1].category != e.category {
			items = append(items, data.Divider{Text: e.category, Hue: categoryHue(e.category)})
		}

		items = append(items, e.item)
	}

	return items
}

func typeName(t reflect.Type) string {
	return t.String()
}

// ungrouped are the entities that are in no group and that are not groups, in name order.
func (p *plugin) ungrouped(c claimed) []string {
	var infos []entity.RegisteredEntityInfo

	for _, info := range p.entities() {
		if c.ids[info.Id] || (info.EntityType != nil && info.EntityType.AssignableTo(group.IGroupType)) {
			continue
		}

		infos = append(infos, info)
	}

	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].Name != infos[j].Name {
			return infos[i].Name < infos[j].Name
		}

		return infos[i].Id < infos[j].Id
	})

	ids := make([]string, len(infos))
	for i, info := range infos {
		ids[i] = info.Id
	}

	return ids
}

func (p *plugin) crumbs(g *Group) []data.Crumb {
	var crumbs []data.Crumb

	for at := g; at != nil; at = at.parent {
		crumb := data.Crumb{Label: at.Name(), Href: p.href(at)}
		if at == g {
			crumb.Href = ""
		}

		crumbs = append([]data.Crumb{crumb}, crumbs...)
	}

	return crumbs
}

// notes are what to tell about the group's page: entities configured for it that aren't there.
func (p *plugin) notes(c claimed, g *Group) []string {
	var missing []string

	g.walk(func(d *Group) {
		for _, ref := range c.unresolved[d] {
			if named, ok := ref.(*Named); ok {
				missing = append(missing, fmt.Sprintf("%q", named.Name()))
			} else {
				missing = append(missing, "an entity")
			}
		}
	})

	if len(missing) == 0 {
		return nil
	}

	return []string{fmt.Sprintf("%d configured %s not registered: %s. %s appear when %s plugin has started, if %s exist.",
		len(missing), plural(len(missing), "entity is", "entities are"), join(missing),
		plural(len(missing), "It will", "They will"), plural(len(missing), "its", "their"), plural(len(missing), "it does", "they do"))}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}

func join(items []string) string {
	text := ""

	for i, item := range items {
		if i > 0 {
			text += ", "
		}

		text += item
	}

	return text
}

// groupStub is a group as an entity (see group.IGroup). It reads the tree, which doesn't change, and asks the server what exists, so
// it is safe from any goroutine.
type groupStub struct {
	p *plugin
	g *Group
}

var _ group.IGroup = (*groupStub)(nil)

func (s *groupStub) GetGroupData() group.Group {
	c := s.p.claim()
	data := group.Group{Name: s.g.Name(), Path: s.g.Path()}

	if s.g.parent != nil {
		data.Parent = s.g.parent.Path()
	}

	for _, child := range s.g.children {
		data.Children = append(data.Children, child.Path())
	}

	for _, m := range c.members[s.g] {
		data.Entities = append(data.Entities, m.id)
	}

	return data
}

func (s *groupStub) Descendants() []string {
	c := s.p.claim()

	var ids []string

	s.g.walk(func(d *Group) {
		for _, m := range c.members[d] {
			ids = append(ids, m.id)
		}
	})

	return ids
}
