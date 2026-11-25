package provider

import "github.com/co-native/git-work/internal/config"

// Identity is a ticket's resolved identity: what a work folder records in its
// state file and renders into its generated files. ProviderName and
// Description stay empty when the provider could not be reached, which is the
// state `new` records in that case.
type Identity struct {
	DisplayID    string
	Title        string // the fetched title, or DisplayID when there is none
	Description  string
	ProviderName string
	FolderCase   string
	BranchCase   string
}

// ResolveIdentity resolves id to a provider and fetches the ticket's details.
// A failed fetch is reported to warn and leaves Title at the display id; the
// caller continues with what it has, as `new` does. matched reports whether a
// configured provider matched; each caller decides the policy (`new` suggests
// --no-ticket, `rename` names its own remedy).
func ResolveIdentity(provs []config.Provider, id string, warn func(string)) (in Identity, matched bool, err error) {
	if err := Validate(provs); err != nil {
		return Identity{}, false, err
	}
	match, err := Resolve(provs, id)
	if err != nil {
		return Identity{}, false, err
	}
	in = Identity{DisplayID: DisplayID(id)}
	in.Title = in.DisplayID
	if match.Provider == nil {
		return in, false, nil
	}
	in.FolderCase, in.BranchCase = match.Provider.FolderCase, match.Provider.BranchCase
	d, err := Fetch(match, id)
	if err != nil {
		if warn != nil {
			warn(err.Error())
		}
		return in, true, nil
	}
	in.ProviderName = match.Provider.Name
	if d.Title != "" {
		in.Title = d.Title
	}
	in.Description = d.Description
	return in, true, nil
}
