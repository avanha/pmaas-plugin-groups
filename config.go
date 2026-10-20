package groups

// PluginConfig configures the plugin. Use NewPluginConfig: it has the root group, which is what the tree is built under.
type PluginConfig struct {
	// ShowUngrouped is that an entity in no group is shown on the root's page, if its plugin gave it a view. What has none, such as
	// the server's clock, is not. The default is true: a thermometer that turns up in the house appears without being asked for.
	ShowUngrouped bool

	// Name is what the plugin is called in the menu. The default is "Groups".
	Name string

	tree *tree
}

// tree is shared by the copies of the config, so that a ref made from one is found by the plugin made from another.
type tree struct {
	root  *Group
	named []*Named
}

func NewPluginConfig() PluginConfig {
	return PluginConfig{ShowUngrouped: true, Name: "Groups", tree: &tree{root: newGroup(RootName, nil)}}
}

// Root is the group everything is under, called "All". Make the groups with Group.
func (c PluginConfig) Root() *Group {
	return c.tree.root
}

// Named refers to an entity by its name, for one with no handle. The group takes it with Add.
func (c PluginConfig) Named(name string) *Named {
	n := &Named{name: name}
	c.tree.named = append(c.tree.named, n)

	return n
}
