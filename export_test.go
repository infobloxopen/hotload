package hotload

// ResetHooks removes all registered hooks. Exported to the external test
// package only; tests register per-test hooks and must not see hooks from
// earlier tests.
var ResetHooks = resetHooks
