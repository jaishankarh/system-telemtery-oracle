package grouping

// DefaultLayers is the built-in chain, in the order of SPEC section 6.
// Insert new layers at their slot; first match wins.
//
//	user rules from the config file   (later ticket, before App Profiles)
//	Container / Pod                   (later ticket)
//	App Profiles                      (later tickets)
//	Dev & Build per Project           (later ticket)
//	kernel threads
//	systemd unit, snap, flatpak
//	top ancestor
//
// Other is not a layer: the Grouper puts there whatever no layer names.
func DefaultLayers() []Layer {
	return []Layer{
		&ContainerPod{},
		KernelThreads{},
		CgroupUnit{},
		TopAncestor{},
	}
}
