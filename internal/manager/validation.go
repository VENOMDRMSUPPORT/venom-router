package manager

import (
	"fmt"
	"strings"
)

// ValidateDefinitions checks cross-group safety constraints before the first
// process is spawned. Empty ports and data roots are ignored because a product
// may intentionally report a value only through its own adapter.
func ValidateDefinitions(definitions []GroupDefinition) error {
	groups := make(map[GroupID]struct{}, len(definitions))
	ports := make(map[string]GroupID)
	dataRoots := make(map[string]GroupID)
	for _, definition := range definitions {
		if err := definition.Validate(); err != nil {
			return err
		}
		if _, exists := groups[definition.ID]; exists {
			return fmt.Errorf("%w: duplicate group %q", ErrInvalidDefinition, definition.ID)
		}
		groups[definition.ID] = struct{}{}
		for _, service := range definition.Services {
			for _, port := range service.Ports {
				port = strings.TrimSpace(port)
				if port == "" {
					continue
				}
				if owner, exists := ports[port]; exists && owner != definition.ID {
					return fmt.Errorf("%w: port %q is claimed by %q and %q", ErrInvalidDefinition, port, owner, definition.ID)
				}
				ports[port] = definition.ID
			}
			root := strings.TrimSpace(service.DataRoot)
			if root == "" {
				continue
			}
			if owner, exists := dataRoots[root]; exists && owner != definition.ID {
				return fmt.Errorf("%w: data root %q is claimed by %q and %q", ErrInvalidDefinition, root, owner, definition.ID)
			}
			dataRoots[root] = definition.ID
		}
	}
	return nil
}
