# pmaas-plugin-groups
A PMAAS plugin that organises entities into a tree of groups, with a page to walk it. The assembly builds the tree and puts entities in
it; nothing is discovered.

```go
conf := groups.NewPluginConfig()
cougar := conf.Root().Group("Cougar") // the root, "All", always exists
cougar.Group("Master Bedroom").Add(bedroomFan, masterBedroomThermometer)
cougar.Group("Living Room").Add(livingRoomFan, conf.Named("Couch plug"))
conf.Root().Group("Oasis")

// After the plugins it groups, so that it is stopped before them.
coreConfig.AddPlugin(groups.NewPlugin(conf), config.PluginConfig{})
```

## Putting entities in groups

`Add` takes an `entity.Ref`: the handle a plugin's config gave back for the entity (`conf.Fan(...)` of the Bond plugin, `AddNativeDevice`
of ESPHome, and so on). The entity doesn't exist when the assembly is configured, so a handle names it, and is resolved to the entity's id
whenever a page is made. For an entity with no handle, `conf.Named("name")` finds the one entity registered with that name (none or
several is not a match, and the page says some entities are missing).

* An entity is in **one** group. If it is added to two, the first, in the order the tree is walked, keeps it.
* `AddHidden` puts an entity in a group without showing it on the page, for what asks what the group holds.
* An entity in no group appears on the root's page if its plugin gave it a view (`ShowUngrouped`, on by default). Entities with no
  view, such as the server's clock, never do.
* A group name must not be empty or contain a `/`, and a `nil` ref is refused. Both panic when the config is built.

## The page

`/plugins/groups/` is the root, `?g=Cougar/Master Bedroom` a group. It shows breadcrumbs to walk back up, the groups directly under it as
tiles with counts, the group's own entities, and then, under "In the groups below", those of the groups below it. Entities are listed by
kind, fans together and switches together, each kind under a heading and in name order. `CategoryOrder` sets the order of the kinds
(default: Fans, Lights, Switches, Thermometers, Sensors, Power, Buttons; "Other" is last).

The menu entry is "Groups", with priority 70, below Status, Alerts and Automations. The assembly can change it with
`config.PluginConfig{MenuPriority: &p}`.

## Groups are entities

Each group is registered as a `group.IGroup` (see `pmaas-spi/group`): its path, its parent, its children, the ids of the entities directly
in it, and `Descendants()` for all of them. So another plugin or an automation can ask what is in "Cougar/Master Bedroom".

## What a plugin does to be shown

A group page draws each entity with the card its own plugin draws, so a plugin has to say how:

1. Its handle for the entity implements `entity.Ref` (`spicommon.StubHolder` already has `EntityId`, so a handle that has one forwards it).
2. After `RegisterEntity` the plugin calls `container.RegisterEntityView(id, fn)`. `fn` runs on the plugin's goroutine and returns an
   `spi.EntityView`: the value its card renderer takes, a `Key` (entities with the same key are one card: a device and the entities
   inside it are drawn once) and a `Category` (`group.CategoryFans` and so on).

An entity with no view that was put in a group is shown as a small summary: its name, its type and a link to its plugin's page.
