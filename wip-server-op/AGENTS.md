# Agent Guidelines - Go Server Development

## PRIMARY OBJECTIVE
Add features to the Go server gradually, maintaining consistency with the existing Go codebase.
The Node.js operator-api is PROVIDED FOR REFERENCE ONLY to understand feature implementations.

## Build/Test Commands (Go Server)
- `make test` - Run all tests with race detection
- `go test -v ./cmd/api/api -run TestName` - Run specific test
- `go test -v ./path/to/package` - Run package tests
- `make build` - Build server binary
- `make dev` - Run development server
- `make oapi-generate` - Regenerate OpenAPI code after updating openapi.yaml

## IMPORTANT: Go Version
- ALWAYS use Go 1.24.3
- If modifying go.mod, maintain "go 1.24.3" directive
- If encountering Go version issues, reinstall Go 1.24.3
- DO NOT downgrade the Go version in go.mod
- Test with the correct Go version to avoid compatibility issues
- IDE/editor diagnostics may show errors with older Go versions - verify with actual Go 1.24.3 compilation

## Go Code Style
- **Package Structure**: Follow existing pattern in cmd/api/api/
- **Imports**: Group by stdlib, external deps, internal packages
- **Error Handling**: Return explicit errors, use fmt.Errorf for wrapping
- **Testing**: Use testify/assert and testify/require, create mocks as needed
- **OpenAPI**: Implement StrictServerInterface methods matching oapi specs
- **Context**: Pass context.Context, use logger.FromContext for logging
- **Naming**: Follow Go conventions (exported/unexported), match existing patterns

## CRITICAL: OpenAPI Consistency
**EVERY feature addition/edit MUST be reflected in server/openapi.yaml:**
- Add/update endpoint paths and operations
- Define request/response schemas
- Update component schemas as needed
- Run `make oapi-generate` after OpenAPI changes
- Implement generated interface methods in cmd/api/api/

### IMPORTANT: OpenAPI Generation Issues
- oapi-codegen may not work correctly in all environments
- ALWAYS verify generated Go code matches your OpenAPI spec
- If generation fails, manually implement required types/interfaces
- **Safety Process**:
  1. First add the Go types/structs that match new OpenAPI schemas
  2. Create placeholder handlers that return dummy responses
  3. Verify type compatibility before implementing actual functionality
  4. Ensure 100% coherence between OpenAPI spec and Go implementation
  5. Test with curl commands to verify HTTP responses before implementing complex logic
  6. When in doubt, manually verify generated code against your schema definitions

## Development Workflow
1. Check Node.js implementation in operator-api/src/routes/ for feature behavior
2. Update server/openapi.yaml with new endpoints/schemas
3. Run `make oapi-generate` to generate Go interfaces
4. Verify generated code matches OpenAPI spec
5. Create Go structs and placeholder endpoints first to validate type coherence
6. Implement full functionality in Go following existing patterns in cmd/api/api/
7. Write tests using testify framework
8. Run tests and verify functionality with curl/HTTP requests
9. Ensure make test passes before completing

## Features to Port (Priority Order)
### Already Implemented
- [x] Recording (start/stop/status)
- [x] Filesystem (read/write/list/watch)
- [x] Computer control (xdotool integration)
- [x] Health endpoint
- [x] Screenshot capture endpoints

### To Be Ported
- [ ] Process management (start/stop/list)
- [ ] Input simulation (keyboard/mouse)
- [ ] Clipboard operations
- [ ] Browser HAR capture
- [ ] Network operations
- [ ] Stream management
- [ ] Metrics collection
- [ ] OS information
- [ ] Pipe operations
- [ ] Bus messaging
- [ ] Logs management
- [ ] Macros execution
- [ ] Scripts execution
- [ ] Browser extensions support