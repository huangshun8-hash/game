# Project Development Specifications
- All comments shall be written in Chinese.
- Prioritize Go standard libraries (e.g., slices, maps) to avoid reinventing the wheel.
- Security and performance shall be taken into consideration for all functional code.
- Unified naming convention: use lowercase letters separated by underscores.
- Test files shall end with _test.go.
- Variables and functions shall follow camelCase naming convention.
- All string keys used as identifiers (including but not limited to: JSON tags, BSON tags, Fiber Locals keys, async Context keys, zap log field keys, MongoDB index names, and any other key-value identifiers) MUST use camelCase format (e.g., `userId`, `playerId`, `requestId`, `openId`). snake_case format (e.g., `user_id`, `player_id`) is strictly prohibited.
- Boolean variables and interface return values shall start with prefixes such as Is, Has, Can.
- Maintain consistent capitalization for abbreviations across the codebase.
- Complex interfaces shall include annotations explaining the meanings of input parameters and return values.
- All abstracted and extracted public interfaces and constants must be accompanied by comments. 
- Reference to unknown third-party libraries is prohibited.
- All JSON serialization and deserialization MUST use `github.com/bytedance/sonic` instead of the standard `encoding/json` package. The Fiber server and client are already configured globally with sonic; direct usage should also use `sonic.Marshal` / `sonic.Unmarshal`.
- All messages returned to the client (including error messages, response messages, and validation messages) MUST be in English. Chinese is strictly prohibited in any client-facing output.
- HTTP API URL path segments MUST use camelCase (e.g., `/api/mail/receiveAll`, `/api/user/bindAccount`). snake_case or kebab-case path segments (e.g., `receive_all`, `bind-account`) are strictly prohibited.
- All client-facing HTTP responses MUST use HTTP status code 200, regardless of business outcome (success or error). Business errors MUST be communicated exclusively via the `code` field in the JSON response body. Non-200 HTTP status codes are strictly prohibited for client-facing interfaces. Internal/service-to-service interfaces (e.g., GMT, Platform callbacks) may use appropriate HTTP status codes.
- Refer to design.md for project design specifications and relevant guidelines.
- The project Code Wiki is located at `docs/code_wiki.md`. Review this file first to quickly understand the project structure, architecture, modules, dependencies, and runtime flow.
- When any feature is added, removed, or changed, the corresponding Code Wiki content in `docs/code_wiki.md` MUST be updated synchronously to keep documentation consistent with the codebase.
- All timestamps MUST use milliseconds (Unix milliseconds, i.e., `time.Now().UnixMilli()`). Second-level timestamps (Unix seconds) are strictly prohibited. This applies to all fields in API responses, database documents, proto messages, log fields, and any other place where a timestamp value appears.

## Logging Specifications
- Error and warning logs MUST include sufficient context for troubleshooting and trace correlation.
- For logs related to HTTP requests or external service calls, the following fields MUST be recorded:
  - `requestId`: The request trace ID, used to correlate logs across the call chain.
  - `url` / `path`: The request URL or API path.
  - Request parameters in their original format: JSON body as `reqBody`, or query string as `query` — do NOT split into individual fields. Use structured objects (e.g., `zap.Any`) for logging instead of byte strings. Avoid logging sensitive data such as tokens or passwords.
- For logs involving player or user operations, the following fields MUST be recorded:
  - `requestId`: The request trace ID.
  - `userId`: The unique identifier of the player or user.
- The `requestId` and `playerID` MUST be passed explicitly from the handler layer down to the logic layer as parameters, rather than being extracted from `context.Context` implicitly.
- For **non-proto request DTOs** (structs embedding `httpx.BaseReq`): the handler MUST pass the entire `req` object to the logic layer. Logic layer functions accept `req *dto.XxxReq` and extract fields internally via `req.PlayerID()`, `req.RequestID()`, etc.
- For **proto request messages** (`*cspb.XxxReq`): because proto-generated structs cannot embed `BaseReq`, the handler MUST read `requestID` and `playerID` from the Fiber context (`c.RequestID()`, `httpx.PlayerIDFromCtx(c)`) and pass them as explicit parameters alongside `req`. Logic layer function signatures MUST be `(ctx context.Context, req *cspb.XxxReq, requestID string, playerID int64)`.
- Log messages MUST be written in Chinese (comments describing the log purpose), while all structured field keys MUST be in English and use camelCase (e.g., `requestId`, `userId`, `url`).

## Protobuf & Generated Code
- The `pb/cspb/` directory contains auto-generated Go code from protobuf definitions. Code review and code inspection MUST ignore all files under `pb/cspb/` — do not lint, audit, or suggest changes to generated files.
- Files under `pb/cspb/` MUST NOT be modified manually. To regenerate them, run the `gen_proto.sh` script at the project root. All protobuf changes MUST go through `.proto` source files followed by script regeneration.
