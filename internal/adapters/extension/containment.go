// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

// EgressContainment describes whether an extension module's process is denied
// *direct* network egress by the operating system — [ADR 0064](0064)'s layer 3,
// the control that turns the forward proxy from the easy path into the only one.
//
// The Platform cannot make this true on its own. Denying a subprocess a network
// of its own is an OS mechanism — a network namespace, a dedicated uid with a
// firewall owner rule, or seccomp on connect(2) — that needs privileges a
// non-root Platform does not have, and on macOS and Windows there is no low-cost
// mechanism at all ([ADR 0080](0080)). So the guarantee is a property of the
// *deployment*, and the honest thing the Platform can do is know and report which
// posture it is in rather than claim enforcement uniformly. That report is this
// type: it is what an admin surface shows as "module egress is enforced" versus
// "attributed but not enforced", and what keeps the layer-3 claim from being made
// where it is not true.
type EgressContainment struct {
	// Enforced is true only where the operating system actually denies a module
	// direct egress, so the proxy is the only path. False means the proxy still
	// sees and attributes every host a *cooperating* module contacts and applies
	// the deny list — strictly better than nothing — but a hostile module could
	// dial out around it.
	Enforced bool
	// Detail is the one honest sentence behind the posture, for a boot log and an
	// admin surface: what is enforced, or why it is not.
	Detail string
}

// EgressEnforcementEnv is the environment variable by which a deployment attests
// that it has put an OS-level egress control in place for module processes. It is
// an attestation, not a switch the Platform can verify from inside its own
// process, which is why the default is the safe one: unset means "attributed but
// not enforced", never a claim the deployment did not make.
const EgressEnforcementEnv = "MOSAIC_MODULE_EGRESS"

// egressEnforcedValue is the one value of EgressEnforcementEnv that declares
// enforcement. Anything else — unset, empty, a typo — is read as not enforced,
// so a misspelling downgrades the claim rather than silently asserting it.
const egressEnforcedValue = "enforced"

// DetermineEgressContainment resolves the posture from the platform the Platform
// runs on and what the deployment declared. It is pure — goos and declared in,
// posture out — so it is decided in one place and testable without an
// environment.
//
// The platform is decisive first: macOS and Windows have no OS-level egress
// control worth relying on (ADR 0080), so a declaration of enforcement there is a
// mistake to surface rather than honour — reporting enforced where it is not is
// exactly what this exists to prevent. On Linux (and other unixes where a
// mechanism can exist) the deployment's attestation is honoured, because there
// the control is real; absent it, the posture is the honest "attributed only".
func DetermineEgressContainment(goos, declared string) EgressContainment {
	switch goos {
	case "darwin", "windows":
		if declared == egressEnforcedValue {
			return EgressContainment{
				Enforced: false,
				Detail: EgressEnforcementEnv + "=" + egressEnforcedValue + " was set, but " + goos +
					" has no OS-level egress control (ADR 0080); reporting attributed-only rather than a guarantee that is not there",
			}
		}
		return EgressContainment{
			Enforced: false,
			Detail:   "no OS-level egress control on " + goos + "; a module's egress is attributed by the proxy but not enforced",
		}
	default:
		if declared == egressEnforcedValue {
			return EgressContainment{
				Enforced: true,
				Detail:   "the deployment attests an OS-level control (network namespace, dedicated-uid firewall, or seccomp on connect) denies a module direct egress; the proxy is the only path",
			}
		}
		return EgressContainment{
			Enforced: false,
			Detail: "not declared: a cooperating module routes through the proxy, but a hostile one could dial out directly. Set " +
				EgressEnforcementEnv + "=" + egressEnforcedValue + " only where the OS denies it (ADR 0080)",
		}
	}
}

// Summary is the one-word posture for a compact display or a log field.
func (c EgressContainment) Summary() string {
	if c.Enforced {
		return "enforced"
	}
	return "attributed but not enforced"
}
