// Package adapters provides structural translations and bridges between different system domains.
//
// It follows the Hexagonal Architecture (Ports and Adapters) pattern to keep core business
// logic isolated from external interfaces and dependencies.
//
// Examples include:
// - Service Adapters: Bridging the YouTube client to the harvester's searcher interface.
// - DB Adapters: Converting internal database records into format-specific objects for other services.
// - Interface Wrappers: Ensuring that low-level utility packages satisfy the requirements of high-level orchestrators.
package adapters
