// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

//go:build !mosaicdev

// The shipped half of the module-repository override (platform#55): there is no
// override. This file is what an ordinary go build compiles, and it is the whole
// of the mechanism in a release — two functions that read nothing and decide
// nothing. devregistry.go carries the reasoning.
//
// official_test.go asserts this by setting both variables and watching the
// compiled-in URL and key come back unchanged, so the guard has a test in the
// build that ships and not only in the one that does not.

package extension

// devRepositoryOverride reports no override, without consulting the
// environment. Deliberately not "reads the environment and ignores it": there is
// no code path here from an environment value to a trusted key, which is a
// stronger statement than any branch could make.
func devRepositoryOverride() (devOverride, bool, error) {
	return devOverride{}, false, nil
}

// officialFetcher returns the guarded fetcher — the only one this build has.
// Module downloads go through netguard's dial guard like every other outbound
// fetch the Platform makes on a user's behalf (platform#50).
func officialFetcher() Fetcher {
	return NewHTTPFetcher()
}
