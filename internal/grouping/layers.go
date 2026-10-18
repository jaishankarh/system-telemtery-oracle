package grouping

// DefaultLayers is the built-in chain, in the order of SPEC section 6.
// Insert new layers at their slot; first match wins.
//
//	Container / Pod                   (ContainerPod)
//	user rules from the config file   (UserRules; Configured fills it; before App Profiles)
//	Dev & Build per Project           (DevBuild; ahead of App Profiles, see its doc comment)
//	App Profiles                      (NewAppProfiles; the table is in profiles_table.go)
//	kernel threads
//	systemd unit, snap, flatpak
//	top ancestor
//
// Other is not a layer: the Grouper puts there whatever no layer names.
func DefaultLayers() []Layer {
	return []Layer{
		&ContainerPod{},
		UserRules{},
		&DevBuild{},
		NewAppProfiles(),
		KernelThreads{},
		CgroupUnit{},
		TopAncestor{},
	}
}
