// Package groups is a plugin that organises entities into a tree of groups the assembly builds, with a page to walk it: the groups
// under the one on show, the entities in it, and then those in the groups below it. Each group is an entity too.
package groups

import (
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"sort"

	"github.com/avanha/pmaas-plugin-groups/data"
	"github.com/avanha/pmaas-plugin-groups/internal/http"
	spi "github.com/avanha/pmaas-spi"
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
			id, ok := m.ref.EntityId()

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

	for _, child := range g.children {
		page.Header.Children = append(page.Header.Children,
			data.ChildGroup{Name: child.Name(), Href: p.href(child), Groups: len(child.children), Entities: c.countShown(child)})
	}

	// What is asked for, in the order it is drawn: the group's own, those of the groups below it, and for the root those in no group.
	type section struct {
		group *Group
		ids   []string
	}

	sections := []section{{group: g, ids: c.shown(g)}}
	for _, d := range subtree(g) {
		sections = append(sections, section{group: d, ids: c.shown(d)})
	}

	var ungrouped []string

	if g.parent == nil && p.config.ShowUngrouped {
		ungrouped = p.ungrouped(c)
	}

	var ids []string
	for _, s := range sections {
		ids = append(ids, s.ids...)
	}

	ids = append(ids, ungrouped...)
	results := make(map[string]spi.EntityViewResult, len(ids))

	for _, result := range p.container.GetEntityViews(ids) {
		results[result.EntityId] = result
	}

	seen := map[string]bool{}

	draw := func(id string, explicit bool) (any, bool) {
		item, key, ok := p.item(results[id], explicit)
		if !ok || seen[key] {
			return nil, false
		}

		seen[key] = true

		return item, true
	}

	// The groups' entities are drawn first, so that a card they own is theirs: a device whose fan is in a group and whose light is in
	// none is one card, and it belongs to the group. What is in no group is drawn last, and is shown after the root's own.
	sectionItems := make([][]any, len(sections))

	for i, s := range sections {
		for _, id := range s.ids {
			if item, ok := draw(id, true); ok {
				sectionItems[i] = append(sectionItems[i], item)
			}
		}
	}

	for _, id := range ungrouped {
		if item, ok := draw(id, false); ok {
			sectionItems[0] = append(sectionItems[0], item)
		}
	}

	for i, s := range sections {
		if len(sectionItems[i]) == 0 {
			continue
		}

		if i > 0 {
			page.Items = append(page.Items, data.Divider{Text: p.relative(g, s.group), Href: p.href(s.group)})
		}

		page.Items = append(page.Items, sectionItems[i]...)
	}

	page.Header.Notes = p.notes(c, g)
	page.Header.Empty = len(page.Items) == 0 && len(page.Header.Children) == 0

	return page, true
}

// item is what the page draws for an entity: its view if its plugin gave one, or else a summary of it, if it was asked for by name.
// The key is the card's: entities that share one are drawn once.
func (p *plugin) item(r spi.EntityViewResult, explicit bool) (item any, key string, ok bool) {
	if r.EntityId == "" {
		return nil, "", false
	}

	if r.View != nil && r.Err == nil {
		key = r.View.Key
		if key == "" {
			key = "entity:" + r.EntityId
		}

		return r.View.Value, key, true
	}

	if !explicit && r.Err == nil {
		return nil, "", false
	}

	summary := data.Summary{Name: r.Name, Plugin: r.Plugin, PluginHref: r.PluginPath}
	if r.EntityType != nil {
		summary.Type = typeName(r.EntityType)
	}

	if r.Err != nil {
		summary.Error = r.Err.Error()
	}

	return summary, "entity:" + r.EntityId, true
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

// relative is the group's path below the group on show, such as "Master Bedroom" or "Upstairs / Loft", for the divider that heads its entities.
func (p *plugin) relative(from, to *Group) string {
	var names []string

	for at := to; at != nil && at != from; at = at.parent {
		names = append([]string{at.Name()}, names...)
	}

	text := ""

	for i, name := range names {
		if i > 0 {
			text += " / "
		}

		text += name
	}

	return text
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
