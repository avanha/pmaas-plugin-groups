package groups

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/avanha/pmaas-spi/entity"
)

// RootName is what the root group is called.
const RootName = "All"

// Group is a group of entities and of groups, built by the assembly. An entity is in one group, directly, and so in all those above
// it. The tree is built before the plugin is added and does not change after.
type Group struct {
	name     string
	parent   *Group
	children []*Group
	members  []member
}

type member struct {
	ref    entity.Ref
	hidden bool
}

func newGroup(name string, parent *Group) *Group {
	return &Group{name: name, parent: parent}
}

// Group is the group of that name directly under this one, made if it is not there yet. It panics on a name that is empty or has a
// "/" in it, which is what paths are made of: this is configuration, and a mistake in it should not go unnoticed.
func (g *Group) Group(name string) *Group {
	if strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
		panic(fmt.Sprintf("groups: %q is not a usable group name: it must not be empty or contain a \"/\"", name))
	}

	for _, child := range g.children {
		if child.name == name {
			return child
		}
	}

	child := newGroup(name, g)
	g.children = append(g.children, child)

	return child
}

// Add puts entities in the group, shown on its page. A ref is what a plugin's config gave back for an entity (a handle), or a Named.
// An entity that is already in a group is not moved: the first to claim it keeps it.
func (g *Group) Add(refs ...entity.Ref) *Group {
	return g.add(false, refs)
}

// AddHidden puts entities in the group without showing them on its page. They are still in it, for what asks what the group holds.
func (g *Group) AddHidden(refs ...entity.Ref) *Group {
	return g.add(true, refs)
}

func (g *Group) add(hidden bool, refs []entity.Ref) *Group {
	for _, ref := range refs {
		if ref == nil {
			panic("groups: a nil ref was added to " + g.describe())
		}

		g.members = append(g.members, member{ref: ref, hidden: hidden})
	}

	return g
}

func (g *Group) describe() string {
	if g.parent == nil {
		return "the root group"
	}

	return "group " + g.Path()
}

// Name is the group's own name.
func (g *Group) Name() string {
	return g.name
}

// Path is the names from the root to the group, joined with "/". The root's is empty.
func (g *Group) Path() string {
	if g.parent == nil {
		return ""
	}

	if g.parent.parent == nil {
		return g.name
	}

	return g.parent.Path() + "/" + g.name
}

// find is the group at the path, or nil.
func (g *Group) find(path string) *Group {
	current := g

	if path == "" {
		return current
	}

	for _, name := range strings.Split(path, "/") {
		var next *Group

		for _, child := range current.children {
			if child.name == name {
				next = child

				break
			}
		}

		if next == nil {
			return nil
		}

		current = next
	}

	return current
}

// walk calls the function for the group and then each below it, depth first, in the order they were made.
func (g *Group) walk(f func(*Group)) {
	f(g)

	for _, child := range g.children {
		child.walk(f)
	}
}

// Named refers to an entity by the name it was registered with, for one that has no handle. It means the entity with that name if there
// is exactly one.
type Named struct {
	name   string
	lookup atomic.Pointer[func(name string) (string, bool)]
}

var _ entity.Ref = (*Named)(nil)

func (n *Named) RegisteredId() (string, bool) {
	lookup := n.lookup.Load()
	if lookup == nil {
		return "", false
	}

	return (*lookup)(n.name)
}

// Name is the name it looks for.
func (n *Named) Name() string {
	return n.name
}
