// Package gent implements the private runtime behind Gent's public contracts and executor.Runner.
// It keeps state reduction, managed operations, and attempt cleanup in one package so restoration
// and live execution use the same invariants. Applications import the root gent package.
package gent
