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
- **OpenAPI**: Implement StrictServerInterface methods matching openapi specs
- **Context**: Pass context.Context, use logger.FromContext for logging
- **Naming**: Follow Go conventions (exported/unexported), match existing patterns

## CRITICAL: OpenAPI Consistency
**EVERY feature addition/edit MUST be reflected in server--{name}/openapi.yaml:**
- Add/update endpoint paths and operations
- Define request/response schemas
- Update component schemas as needed
- Implement generated interface methods in cmd/api/api/

### IMPORTANT: OpenAPI Generation Issues
- oapi-codegen DOES NOT WORK ; you'd need to do what it would do manually
- ALWAYS verify generated Go code matches your OpenAPI spec
- If generation fails, manually implement required types/interfaces
- **Safety Process**:
  1. First add the Go types/structs that match new OpenAPI schemas
  2. Create placeholder handlers that return dummy responses
  3. Verify type compatibility before implementing actual functionality
  4. Ensure 100% coherence between OpenAPI spec and Go implementation
  5. Test with curl commands to verify HTTP responses before implementing complex logic (server should serve at PORT 10001 ; ensure killing running server from ie. previous sessions in case)
  6. When in doubt, manually verify generated code against your schema definitions

## Development Workflow
1. Check Node.js implementation in operator-api/src/routes/ for feature behavior
2. Update server--{name}/openapi.yaml with new endpoints/schemas
3. since `oapi-generate` doesnt work, you'd need to do what it would have done manually and slowly to ensure coherence
4. Verify generated code matches OpenAPI spec
5. Create Go structs and placeholder endpoints first to validate type coherence
6. Implement full functionality in Go following existing patterns in cmd/api/api/
7. Write tests using testify framework
8. Run tests and verify functionality with curl/HTTP requests
9. Ensure make test passes before completing

## Test Details
- When testing in this development environment, you should pass the env variable DISPLAY=:20 .
  In production, the API uses DISPLAY=:1 which is rightfully configured and should not be altered,
  but in this env always pass DISPLAY=:20 when starting the server (it influences even features like clipboard etc not just capture)
- IMPORTANT: Do not add DISPLAY environment variables to READMEs or other documentation files. This is only for testing purposes.

## Features to Port

### Implemented In Base Server
- [x] Recording (start/stop/status)
- [x] Filesystem (read/write/list/watch)
- [x] Computer control (xdotool integration)
- [x] Health endpoint

### Implemented In Cloned Servers
- [x] Screenshot capture endpoints [see : `server--screenshot`]

### To Be Ported In Cloned Servers
- [ ] Process management (start/stop/list)
- [ ] Input simulation (keyboard/mouse)
- [x] Clipboard operations
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


## Important Detail

- Each "PORT" operation should happen in the right `server--{name}` dir.
- Every `server--{name}` dir is a clone of the base image, and should be the dir where the added feature is added
  For example, `server--screenshot` implements the screenshot endpoints
- We made the choice to have distinct dirs , one for each group of features , for atomicity purposes with management.
  When making updates, identify the right target server dir, implement the changes inside it, and build+test the right target server.
- List of clones of the base server you can use as target :
```
server--process
server--clipboard
server--browser
server--network
server--stream
server--metrics
server--os
server--pipe
server--bus
server--logs
server--macros
server--scripts
server--browser-ext
```
