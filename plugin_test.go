package groups

import (
	"errors"
	iofs "io/fs"
	nethttp "net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/avanha/pmaas-plugin-groups/data"
	spi "github.com/avanha/pmaas-spi"
	"github.com/avanha/pmaas-spi/device"
	"github.com/avanha/pmaas-spi/entity"
	"github.com/avanha/pmaas-spi/group"
)

// ref is a handle: it knows its entity's id once the entity is registered.
type ref struct {
	mu sync.Mutex
	id string
}

func (r *ref) RegisteredId() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.id, r.id != ""
}

func (r *ref) set(id string) {
	r.mu.Lock()
	r.id = id
	r.mu.Unlock()
}

// fakeContainer is the server as far as the plugin sees it: the entities registered, and a view for some.
type fakeContainer struct {
	spi.IPMAASContainer

	entities []entity.RegisteredEntityInfo
	views    map[string]spi.EntityView
	errs     map[string]error
	asked    [][]string
	reg      map[string]reflect.Type
}

func (f *fakeContainer) RouteFullPath(relative string) string { return "/plugins/groups/" + relative }

func (f *fakeContainer) GetEntities(predicate func(*entity.RegisteredEntityInfo) bool) ([]entity.RegisteredEntityInfo, error) {
	var out []entity.RegisteredEntityInfo

	for i := range f.entities {
		if predicate(&f.entities[i]) {
			out = append(out, f.entities[i])
		}
	}

	return out, nil
}

func (f *fakeContainer) GetEntityViews(ids []string) []spi.EntityViewResult {
	f.asked = append(f.asked, ids)

	results := make([]spi.EntityViewResult, len(ids))

	for i, id := range ids {
		r := spi.EntityViewResult{EntityId: id, Name: id, Plugin: "p", PluginPath: "/plugins/p/"}

		for _, e := range f.entities {
			if e.Id == id {
				r.Name, r.EntityType = e.Name, e.EntityType
			}
		}

		if v, ok := f.views[id]; ok {
			r.View = &v
		}

		r.Err = f.errs[id]
		results[i] = r
	}

	return results
}

func (f *fakeContainer) RegisterEntity(unique string, t reflect.Type, name string, _ spi.EntityStubFactoryFunc) (string, error) {
	if f.reg == nil {
		f.reg = map[string]reflect.Type{}
	}

	id := "group_" + unique
	f.reg[id] = t

	return id, nil
}

func (f *fakeContainer) DeregisterEntity(id string) error { delete(f.reg, id); return nil }

func (f *fakeContainer) ClosedCallbackChannel() chan func() {
	c := make(chan func())
	close(c)
	return c
}

func entityInfo(id, name string) entity.RegisteredEntityInfo {
	return entity.RegisteredEntityInfo{Id: id, Name: name, EntityType: reflect.TypeFor[string]()}
}

// house is a tree like the assembly's: Cougar with two rooms, and Oasis.
type house struct {
	conf                   PluginConfig
	cougar, master, living *Group
	oasis                  *Group
	fan, thermo, tv, pump  *ref
	p                      *plugin
	c                      *fakeContainer
}

func newHouse(t *testing.T) *house {
	t.Helper()

	h := &house{conf: NewPluginConfig(), fan: &ref{}, thermo: &ref{}, tv: &ref{}, pump: &ref{}}
	h.cougar = h.conf.Root().Group("Cougar")
	h.master = h.cougar.Group("Master Bedroom").Add(h.fan, h.thermo)
	h.living = h.cougar.Group("Living Room").Add(h.tv)
	h.oasis = h.conf.Root().Group("Oasis").Add(h.pump)

	h.c = &fakeContainer{
		views: map[string]spi.EntityView{},
		entities: []entity.RegisteredEntityInfo{
			entityInfo("fan", "Bedroom Fan"), entityInfo("thermo", "Master Bedroom"), entityInfo("tv", "TV"), entityInfo("pump", "Pool Pump"),
		},
	}
	h.fan.set("fan")
	h.thermo.set("thermo")
	h.tv.set("tv")
	h.pump.set("pump")

	h.p = NewPlugin(h.conf).(*plugin)
	h.p.Init(&initContainer{fakeContainer: h.c})

	return h
}

// initContainer takes the calls Init makes that the page tests don't care about.
type initContainer struct{ *fakeContainer }

func (initContainer) ProvideContentFS(iofs.FS, string) {}
func (initContainer) EnableStaticContent(string)       {}
func (initContainer) AddRouteWithOptions(string, nethttp.HandlerFunc, *spi.HttpHandlerOptions) {
}
func (initContainer) RegisterEntityRenderer(reflect.Type, spi.EntityRendererFactory) {}

func TestTree_PathsAndNames(t *testing.T) {
	conf := NewPluginConfig()
	room := conf.Root().Group("Cougar").Group("Master Bedroom")

	if conf.Root().Path() != "" || conf.Root().Name() != "All" || room.Path() != "Cougar/Master Bedroom" || conf.Root().Group("Cougar").Path() != "Cougar" {
		t.Errorf("%q %q %q", conf.Root().Path(), room.Path(), conf.Root().Group("Cougar").Path())
	}

	if conf.Root().find("Cougar/Master Bedroom") != room || conf.Root().find("Cougar/Nope") != nil || conf.Root().find("") != conf.Root() {
		t.Error("find")
	}

	if conf.Root().Group("Cougar") != conf.Root().Group("Cougar") {
		t.Error("a group made twice should be one")
	}
}

func TestTree_ABadNameOrRefPanics(t *testing.T) {
	root := NewPluginConfig().Root()

	for name, f := range map[string]func(){
		"empty":     func() { root.Group("  ") },
		"slash":     func() { root.Group("a/b") },
		"nil ref":   func() { root.Add(nil) },
		"nil named": func() { root.AddHidden(nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

func TestPage_ShowsChildGroupsThenTheEntitiesOfTheGroupsBelowGroupedByKindAndName(t *testing.T) {
	h := newHouse(t)
	h.c.views["fan"] = spi.EntityView{Key: "dev/fan", Value: "FAN CARD"}
	h.c.views["thermo"] = spi.EntityView{Value: "THERMO CARD"}
	h.c.views["tv"] = spi.EntityView{Value: "TV CARD"}

	page, ok := h.p.Page("Cougar")
	if !ok {
		t.Fatal("no page")
	}

	if page.Title != "Cougar" || len(page.Header.Children) != 2 || page.Header.Children[0].Name != "Master Bedroom" || page.Header.Children[0].Entities != 2 {
		t.Errorf("%+v", page.Header)
	}

	if len(page.Header.Crumbs) != 2 || page.Header.Crumbs[0].Label != "All" || page.Header.Crumbs[0].Href == "" ||
		page.Header.Crumbs[1].Label != "Cougar" || page.Header.Crumbs[1].Href != "" {
		t.Errorf("%+v", page.Header.Crumbs)
	}

	// Nothing is directly in Cougar, so it is all from the rooms: one list, by kind and then name, with no heading for the rooms.
	want := []any{kindDivider("Other"), "FAN CARD", "THERMO CARD", "TV CARD"}

	if !reflect.DeepEqual(page.Items, want) {
		t.Errorf("got %#v\nwant %#v", page.Items, want)
	}
}

func TestPage_DirectEntitiesComeBeforeThoseBelow(t *testing.T) {
	h := newHouse(t)
	extra := &ref{}
	extra.set("extra")
	h.cougar.Add(extra)
	h.c.entities = append(h.c.entities, entityInfo("extra", "Hall light"))
	h.c.views["extra"] = spi.EntityView{Value: "HALL"}
	h.c.views["fan"] = spi.EntityView{Value: "FAN"}

	page, _ := h.p.Page("Cougar")

	// The group's own, under their kind, then a heading, and the rooms' under theirs.
	if len(page.Items) < 5 || page.Items[0] != (kindDivider("Other")) || page.Items[1] != "HALL" ||
		page.Items[2] != (belowDivider) || page.Items[3] != (kindDivider("Other")) {
		t.Fatalf("%#v", page.Items)
	}
}

func TestPage_OneCardIsDrawnOnceWhateverNumberOfEntitiesShareItsKey(t *testing.T) {
	h := newHouse(t)
	h.c.views["fan"] = spi.EntityView{Key: "device/1", Value: "DEVICE CARD"}
	h.c.views["thermo"] = spi.EntityView{Key: "device/1", Value: "DEVICE CARD"}

	page, _ := h.p.Page("Cougar/Master Bedroom")

	if !reflect.DeepEqual(page.Items, []any{kindDivider("Other"), "DEVICE CARD"}) {
		t.Errorf("%#v", page.Items)
	}
}

func TestPage_AnEntityWithoutAViewGetsASummaryAndAHiddenOneNothing(t *testing.T) {
	h := newHouse(t)
	secret := &ref{}
	secret.set("secret")
	h.c.entities = append(h.c.entities, entityInfo("secret", "Secret"))
	h.master.AddHidden(secret)
	h.c.views["thermo"] = spi.EntityView{Value: "THERMO"}

	page, _ := h.p.Page("Cougar/Master Bedroom")

	summary, ok := page.Items[1].(data.Summary)
	if !ok || summary.Name != "Bedroom Fan" || summary.Plugin != "p" || summary.Type != "string" || len(page.Items) != 3 || page.Items[2] != "THERMO" {
		t.Errorf("%#v", page.Items)
	}

	for _, ids := range h.c.asked {
		for _, id := range ids {
			if id == "secret" {
				t.Error("a hidden entity's view was asked for")
			}
		}
	}
}

func TestPage_ARefThatIsNotRegisteredYetIsNotedNotShown(t *testing.T) {
	h := newHouse(t)
	late := h.conf.Named("Nonexistent Thing")
	h.master.Add(late)
	h.c.views["fan"] = spi.EntityView{Value: "FAN"}

	page, _ := h.p.Page("Cougar/Master Bedroom")

	if len(page.Header.Notes) != 1 || !strings.Contains(page.Header.Notes[0], "Nonexistent Thing") {
		t.Errorf("%v", page.Header.Notes)
	}

	other, _ := h.p.Page("Oasis")
	if len(other.Header.Notes) != 0 {
		t.Errorf("a note about another group: %v", other.Header.Notes)
	}
}

func TestPage_NamedFindsTheOneEntityWithThatNameAndRefusesAnAmbiguousName(t *testing.T) {
	h := newHouse(t)
	h.c.entities = append(h.c.entities, entityInfo("a1", "Porch Light"), entityInfo("b1", "Twin"), entityInfo("b2", "Twin"))
	porch, twin := h.conf.Named("Porch Light"), h.conf.Named("Twin")

	// Init binds the lookups of the names in the config when the plugin is made, so make it again.
	h.p = NewPlugin(h.conf).(*plugin)
	h.p.Init(&initContainer{fakeContainer: h.c})

	if id, ok := porch.RegisteredId(); !ok || id != "a1" {
		t.Errorf("%q %v", id, ok)
	}

	if _, ok := twin.RegisteredId(); ok {
		t.Error("an ambiguous name was resolved")
	}

	if _, ok := h.conf.Named("Nobody").RegisteredId(); ok {
		t.Error("an unbound name was resolved")
	}
}

func TestPage_TheRootShowsUngroupedEntitiesThatHaveViewsAndNotGroupsOrThoseWithout(t *testing.T) {
	h := newHouse(t)
	h.c.entities = append(h.c.entities, entityInfo("zed", "Zed"), entityInfo("alpha", "Alpha"), entityInfo("clock", "Clock"),
		entity.RegisteredEntityInfo{Id: "g", Name: "A group", EntityType: group.IGroupType})
	h.c.views["zed"] = spi.EntityView{Value: "ZED"}
	h.c.views["alpha"] = spi.EntityView{Value: "ALPHA"}
	h.c.views["g"] = spi.EntityView{Value: "A GROUP"}

	page, _ := h.p.Page("")

	// The root's own come first, alphabetical by name, then the groups' (whose entities have no views here, so they are summaries).
	// Nothing is drawn for the clock (no view), the group, or what is in a group.
	if len(page.Items) < 4 || page.Items[1] != "ALPHA" || page.Items[2] != "ZED" {
		t.Errorf("%#v", page.Items)
	}

	if contains(page.Items, "A GROUP") || contains(page.Items, data.Summary{Name: "Clock", Type: "string", Plugin: "p", PluginHref: "/plugins/p/"}) {
		t.Errorf("a group or an entity without a view was shown: %#v", page.Items)
	}

	h.p.config.ShowUngrouped = false

	if page, _ = h.p.Page(""); contains(page.Items, "ALPHA") || contains(page.Items, "ZED") {
		t.Errorf("ungrouped entities shown when switched off: %#v", page.Items)
	}
}

func TestPage_NoSuchGroupIsNotFoundAndAnEmptyOneSaysSo(t *testing.T) {
	h := newHouse(t)

	if _, ok := h.p.Page("Cougar/Garage"); ok {
		t.Error("found a group that isn't there")
	}

	h.conf.Root().Group("Big Horn")

	page, ok := h.p.Page("Big Horn")
	if !ok || !page.Header.Empty {
		t.Errorf("%v %+v", ok, page.Header)
	}
}

func TestPage_AnEntityInTwoGroupsStaysInTheFirst(t *testing.T) {
	h := newHouse(t)
	h.living.Add(h.fan)
	h.c.views["fan"] = spi.EntityView{Value: "FAN"}

	first, _ := h.p.Page("Cougar/Master Bedroom")
	second, _ := h.p.Page("Cougar/Living Room")

	if !contains(first.Items, "FAN") || contains(second.Items, "FAN") {
		t.Errorf("%#v | %#v", first.Items, second.Items)
	}
}

func contains(items []any, want any) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}

	return false
}

func TestPage_AViewThatFailedGivesASummaryWithTheError(t *testing.T) {
	h := newHouse(t)
	h.c.errs = map[string]error{"fan": errors.New("no data")}

	page, _ := h.p.Page("Cougar/Master Bedroom")

	if s, ok := page.Items[1].(data.Summary); !ok || s.Error != "no data" {
		t.Errorf("%#v", page.Items)
	}
}

func TestGroups_AreEntitiesThatSayWhatIsInThem(t *testing.T) {
	h := newHouse(t)
	h.p.Start()

	if len(h.c.reg) != 5 { // All, Cougar, Master Bedroom, Living Room, Oasis
		t.Fatalf("%v", h.c.reg)
	}

	for id, kind := range h.c.reg {
		if kind != group.IGroupType {
			t.Errorf("%s is a %v", id, kind)
		}
	}

	stub := &groupStub{p: h.p, g: h.cougar}
	data := stub.GetGroupData()

	if data.Path != "Cougar" || data.Parent != "" || !reflect.DeepEqual(data.Children, []string{"Cougar/Master Bedroom", "Cougar/Living Room"}) || len(data.Entities) != 0 {
		t.Errorf("%+v", data)
	}

	if got := stub.Descendants(); !reflect.DeepEqual(got, []string{"fan", "thermo", "tv"}) {
		t.Errorf("%v", got)
	}

	h.p.Stop()

	if len(h.c.reg) != 0 {
		t.Errorf("not deregistered: %v", h.c.reg)
	}
}

func TestPage_ACardOwnedByAGroupedEntityIsNotStolenByAnUngroupedOneThatSharesIt(t *testing.T) {
	h := newHouse(t)

	// The fan is in Master Bedroom; its device's light is in no group, and has the same card.
	h.c.entities = append(h.c.entities, entityInfo("light", "Bedroom Light"))
	h.c.views["fan"] = spi.EntityView{Key: "device/1", Value: "DEVICE CARD"}
	h.c.views["light"] = spi.EntityView{Key: "device/1", Value: "DEVICE CARD"}

	root, _ := h.p.Page("")
	count := 0

	for _, item := range root.Items {
		if item == "DEVICE CARD" {
			count++
		}
	}

	if count != 1 {
		t.Fatalf("the card should be drawn once: %#v", root.Items)
	}

	// And it is listed with the rooms' entities, below the heading for them, not among the root's own.
	if root.Items[0] == "DEVICE CARD" {
		t.Errorf("the card should not be first: %#v", root.Items)
	}
}

func typed(id, name string, t reflect.Type) entity.RegisteredEntityInfo {
	return entity.RegisteredEntityInfo{Id: id, Name: name, EntityType: t}
}

func TestPage_EntitiesAreGroupedByKindFansTogetherAndSwitchesTogether(t *testing.T) {
	h := newHouse(t)
	plug, fan2, light := &ref{}, &ref{}, &ref{}
	plug.set("plug")
	fan2.set("fan2")
	light.set("light")
	h.master.Add(plug, fan2, light)
	h.c.entities = []entity.RegisteredEntityInfo{
		typed("fan", "Bedroom Fan", device.IFanType), typed("fan2", "Attic Fan", device.IFanType), typed("plug", "Aquarium Plug", device.ISwitchType),
		typed("light", "Lamp", device.ILightType), entityInfo("thermo", "Master Bedroom"),
	}

	for _, id := range []string{"fan", "fan2", "plug", "light", "thermo"} {
		h.c.views[id] = spi.EntityView{Value: id}
	}

	// A view can say what it is: the thermometer's plugin does, the others are known by their types.
	h.c.views["thermo"] = spi.EntityView{Category: group.CategoryThermometers, Value: "thermo"}

	page, _ := h.p.Page("Cougar/Master Bedroom")

	want := []any{
		kindDivider("Fans"), "fan2", "fan", // by name: Attic Fan, Bedroom Fan
		kindDivider("Lights"), "light",
		kindDivider("Switches"), "plug",
		kindDivider("Thermometers"), "thermo",
	}

	if !reflect.DeepEqual(page.Items, want) {
		t.Errorf("got %#v\nwant %#v", page.Items, want)
	}
}

func TestPage_TheOrderOfKindsCanBeChangedAndOtherIsAlwaysLast(t *testing.T) {
	h := newHouse(t)
	h.p.config.CategoryOrder = []string{group.CategorySwitches, group.CategoryFans}
	h.c.entities = []entity.RegisteredEntityInfo{
		typed("fan", "F", device.IFanType), typed("thermo", "T", device.ISwitchType), typed("tv", "V", device.ILightType),
	}

	for _, id := range []string{"fan", "thermo", "tv"} {
		h.c.views[id] = spi.EntityView{Value: id}
	}

	h.c.views["tv"] = spi.EntityView{Category: "Zebras", Value: "tv"}
	h.living.Add()

	page, _ := h.p.Page("Cougar")

	var kinds []string

	for _, item := range page.Items {
		if d, ok := item.(data.Divider); ok {
			kinds = append(kinds, d.Text)
		}
	}

	// Named kinds first in the order given, then the plugin's own, alphabetically.
	if !reflect.DeepEqual(kinds, []string{"Switches", "Fans", "Zebras"}) {
		t.Errorf("%v", kinds)
	}
}

// kindDivider is the divider the page puts above a kind.
func kindDivider(text string) data.Divider {
	return data.Divider{Text: text, Hue: categoryHue(text)}
}

var belowDivider = data.Divider{Text: "In the groups below", Major: true, Hue: belowHue}

func TestHues_AreStableAndEachKindHasItsOwn(t *testing.T) {
	seen := map[int]string{}

	for category, hue := range categoryHues {
		if other, clash := seen[hue]; clash {
			t.Errorf("%s and %s have the same hue %d", category, other, hue)
		}

		seen[hue] = category
	}

	if hueOf("Zebras") != hueOf("Zebras") || hueOf("Zebras") < 0 || hueOf("Zebras") > 359 || categoryHue("Zebras") != hueOf("Zebras") {
		t.Error("a hue from a name is not stable or not in range")
	}

	if categoryHue(group.CategoryFans) != categoryHues[group.CategoryFans] {
		t.Error("a known kind does not have its own hue")
	}
}

func TestPage_GroupTilesAreDifferentColoursByPosition(t *testing.T) {
	h := newHouse(t)

	page, _ := h.p.Page("Cougar")

	if page.Header.Children[0].Hue != tileHue(0) || page.Header.Children[1].Hue != tileHue(1) || tileHue(0) == tileHue(1) {
		t.Errorf("%+v", page.Header.Children)
	}
}
