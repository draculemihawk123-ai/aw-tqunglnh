package engineeringpack

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// ErrPackCycle is returned by ResolvePackGraph when the pack composition
// graph (the Pack -> Pack edges within packs) contains a cycle. Nothing
// about a cyclic pack composition can be resolved deterministically —
// which pack's dependency declaration should "win" is undefined — so
// ResolvePackGraph fails closed rather than picking an arbitrary
// traversal order (docs/design/04-v2-definition-plane.md V2-06's own
// fail-closed principle, applied to the composition graph's shape
// itself rather than to a resource value collision).
var ErrPackCycle = errors.New("engineeringpack: pack composition graph has a cycle")

// ErrResourceConflict is returned by CheckResourceConflicts the moment
// two resolved resources share a ResourceKey with different content
// while at least one carries definition.PriorityHardConstraint. See
// CheckResourceConflicts' own doc comment for the exact rule.
var ErrResourceConflict = errors.New("engineeringpack: hard-constraint resource conflict")

// PackDependencies is one Engineering Pack version's own directly
// declared dependency pins, keyed by its own EngineeringPackVersionID —
// the same shape EngineeringPackDocument.Dependencies carries for a
// single pack. ResolvePackGraph's caller supplies one entry per pack
// version reachable from root (including root itself); a real caller (a
// future graph/dependency compiler, V2-09) builds this by fetching each
// pack's own published dependency pins from the repository, while this
// package's own tests build it by hand — ResolvePackGraph itself never
// fetches anything, matching this package's own "install/resolve không
// tạo command, gate hay permission grant" boundary (see
// engineeringpack.go's own doc comment).
type PackDependencies map[EngineeringPackVersionID][]definition.DependencyPin

// ResolvedManifest is ResolvePackGraph's deterministic output: every
// pack version the composition actually visited, and every Skill/Layer
// dependency pin reachable from root, deduplicated by Kind+DefinitionID+
// VersionID and sorted into a stable order — lec-04's own Merge policy:
// "Resource list được hợp nhất, khử trùng theo stable ID/version và sắp
// theo thứ tự xác định."
type ResolvedManifest struct {
	Packs     []EngineeringPackVersionID
	Resources []definition.DependencyPin
}

// ResolvePackGraph computes the deterministic, deduplicated set of
// Skill/Layer dependency pins reachable from root by transitively
// following every nested Engineering Pack dependency in packs, or fails
// closed with ErrPackCycle if the pack composition graph is cyclic.
//
// packs should contain an entry for root and for every Engineering Pack
// version transitively reachable from it; a pin naming a pack version
// missing from packs is left unexpanded (its own nested dependencies are
// simply not known to this call) rather than treated as an error — a
// caller resolving a real graph incrementally may not have fetched every
// pack yet, and that is a distinct failure mode from an actual cycle.
func ResolvePackGraph(root EngineeringPackVersionID, packs PackDependencies) (ResolvedManifest, error) {
	if err := detectPackCycle(root, packs); err != nil {
		return ResolvedManifest{}, err
	}

	visitedPacks := map[EngineeringPackVersionID]bool{}
	resourceSet := map[string]definition.DependencyPin{}

	var visit func(id EngineeringPackVersionID)
	visit = func(id EngineeringPackVersionID) {
		if visitedPacks[id] {
			return
		}
		visitedPacks[id] = true
		for _, pin := range packs[id] {
			if pin.Kind == definition.KindEngineeringPack {
				visit(EngineeringPackVersionID(pin.VersionID))
				continue
			}
			resourceSet[resourcePinKey(pin)] = pin
		}
	}
	visit(root)

	manifest := ResolvedManifest{
		Packs:     make([]EngineeringPackVersionID, 0, len(visitedPacks)),
		Resources: make([]definition.DependencyPin, 0, len(resourceSet)),
	}
	for id := range visitedPacks {
		manifest.Packs = append(manifest.Packs, id)
	}
	sort.Slice(manifest.Packs, func(i, j int) bool { return manifest.Packs[i] < manifest.Packs[j] })

	for _, pin := range resourceSet {
		manifest.Resources = append(manifest.Resources, pin)
	}
	sort.Slice(manifest.Resources, func(i, j int) bool {
		return resourcePinKey(manifest.Resources[i]) < resourcePinKey(manifest.Resources[j])
	})

	return manifest, nil
}

func resourcePinKey(pin definition.DependencyPin) string {
	return string(pin.Kind) + ":" + pin.DefinitionID + ":" + pin.VersionID
}

// detectPackCycle runs Tarjan's strongly-connected-components algorithm
// (adapted from internal/domain/workflow/validation.go's own
// stronglyConnectedComponents, which does the same thing over workflow
// node/edge graphs) restricted to the Pack -> Pack edges reachable from
// root: any component with more than one member, or a single-member
// component with a self-edge, is a cycle.
func detectPackCycle(root EngineeringPackVersionID, packs PackDependencies) error {
	index := 0
	indices := map[EngineeringPackVersionID]int{}
	lowLinks := map[EngineeringPackVersionID]int{}
	onStack := map[EngineeringPackVersionID]bool{}
	stack := make([]EngineeringPackVersionID, 0)
	var cyclic []EngineeringPackVersionID

	var visit func(id EngineeringPackVersionID)
	visit = func(id EngineeringPackVersionID) {
		if cyclic != nil {
			return
		}
		indices[id] = index
		lowLinks[id] = index
		index++
		stack = append(stack, id)
		onStack[id] = true

		for _, pin := range packs[id] {
			if pin.Kind != definition.KindEngineeringPack {
				continue
			}
			target := EngineeringPackVersionID(pin.VersionID)
			if _, known := packs[target]; !known {
				continue // not fetched/known to this call, not a detectable cycle
			}
			if _, seen := indices[target]; !seen {
				visit(target)
				if cyclic != nil {
					return
				}
				if lowLinks[target] < lowLinks[id] {
					lowLinks[id] = lowLinks[target]
				}
			} else if onStack[target] && indices[target] < lowLinks[id] {
				lowLinks[id] = indices[target]
			}
		}

		if lowLinks[id] != indices[id] {
			return
		}
		component := make([]EngineeringPackVersionID, 0)
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == id {
				break
			}
		}
		if len(component) > 1 {
			cyclic = component
			return
		}
		// A single-member component is only a cycle if it has a
		// self-edge (a pack pinning itself as an Engineering Pack
		// dependency at its own version id).
		for _, pin := range packs[component[0]] {
			if pin.Kind == definition.KindEngineeringPack && EngineeringPackVersionID(pin.VersionID) == component[0] {
				cyclic = component
				return
			}
		}
	}

	visit(root)
	if cyclic == nil {
		return nil
	}
	sorted := append([]EngineeringPackVersionID(nil), cyclic...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	names := make([]string, len(sorted))
	for i, id := range sorted {
		names[i] = string(id)
	}
	return fmt.Errorf("%w: [%s]", ErrPackCycle, strings.Join(names, ","))
}

// CheckResourceConflicts inspects every resolved resource pulled in by a
// resolved pack manifest (skill.ResourceIdentities / layer.ResourceIdentities
// output, for each Skill/Layer pin ResolvePackGraph returned) and fails
// closed (HE-04-M05: "resolver MUST phát hiện rule/config mâu thuẫn; giá
// trị đơn xung đột không được tự 'last wins'"; lec-04's Merge policy:
// "Hai hard constraint áp dụng đồng thời nhưng mâu thuẫn gây
// configuration error; resolver không tự chọn bên thắng") the moment two
// resources declare the same ResourceKey with a different ContentHash
// while at least one of them is definition.PriorityHardConstraint.
//
// Two resources with the same ResourceKey and the same ContentHash are
// not a conflict: two Skill/Layer versions can independently declare a
// resource under the same key with byte-identical content (e.g. a
// shared baseline both re-publish unchanged), and that must stay
// allowed rather than rejected.
//
// Only HARD_CONSTRAINT conflicts fail closed here — this is V2-06's own
// declared scope ("hard-constraint conflict fail closed"), not a claim
// that REQUIRED_PROCEDURE/GUIDANCE/REFERENCE disagreements are
// harmless. Detecting and surfacing those is left to a later task (e.g.
// HE-04-S04's stale/duplicate/contradictory rule audit): this task's own
// acceptance test (lec-04's "Phép thử chấp nhận" #2) only exercises the
// hard-constraint case, so that is the one this function actually
// enforces.
func CheckResourceConflicts(resources []definition.ResolvedResource) error {
	byKey := make(map[string][]definition.ResolvedResource, len(resources))
	for _, resource := range resources {
		key := resource.Identity.ResourceKey
		byKey[key] = append(byKey[key], resource)
	}

	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		group := byKey[key]
		hasHardConstraint := false
		contentHashes := map[string]bool{}
		for _, resource := range group {
			contentHashes[resource.Identity.ContentHash] = true
			if resource.Priority == definition.PriorityHardConstraint {
				hasHardConstraint = true
			}
		}
		if hasHardConstraint && len(contentHashes) > 1 {
			return fmt.Errorf("%w: resource key %q has %d conflicting content hashes with a HARD_CONSTRAINT among them",
				ErrResourceConflict, key, len(contentHashes))
		}
	}
	return nil
}
