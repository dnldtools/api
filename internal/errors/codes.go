package errors

type Category string

const (
	CategoryValidation          Category = "VALIDATION"
	CategoryAuthentication      Category = "AUTHENTICATION"
	CategoryAuthorization       Category = "AUTHORIZATION"
	CategoryBadRequest          Category = "BAD_REQUEST"
	CategoryNotFound            Category = "NOT_FOUND"
	CategoryConflict            Category = "CONFLICT"
	CategoryRateLimit           Category = "RATE_LIMIT"
	CategoryTimeout             Category = "TIMEOUT"
	CategoryNetwork             Category = "NETWORK"
	CategoryBrowser             Category = "BROWSER"
	CategoryProvider            Category = "PROVIDER"
	CategoryDownloader          Category = "DOWNLOADER"
	CategoryUnsupportedPlatform Category = "UNSUPPORTED_PLATFORM"
	CategoryUnavailableService  Category = "UNAVAILABLE_SERVICE"
	CategoryInternal            Category = "INTERNAL"
)

type Code string

const (
	CodeValidationError         Code = "VALIDATION_ERROR"
	CodeInvalidRequest          Code = "INVALID_REQUEST"
	CodeInvalidURL              Code = "INVALID_URL"
	CodeInvalidJSON             Code = "INVALID_JSON"
	CodeMissingParameter        Code = "MISSING_PARAMETER"
	CodeUnsupportedPlatform     Code = "UNSUPPORTED_PLATFORM"
	CodeYouTubeSeparateEndpoint Code = "YOUTUBE_SEPARATE_ENDPOINT"
	CodeFormatNotAvailable      Code = "FORMAT_NOT_AVAILABLE"

	CodeAuthenticationRequired Code = "AUTHENTICATION_REQUIRED"
	CodeInvalidAPIKey          Code = "INVALID_API_KEY"
	CodeForbidden              Code = "FORBIDDEN"

	CodeAPIKeyMissing    Code = "API_KEY_MISSING"
	CodeAPIKeyInvalid    Code = "API_KEY_INVALID"
	CodeAPIKeyExpired    Code = "API_KEY_EXPIRED"
	CodeAPIKeyRevoked    Code = "API_KEY_REVOKED"
	CodeAccountDisabled  Code = "ACCOUNT_DISABLED"
	CodeAccountSuspended Code = "ACCOUNT_SUSPENDED"

	CodeNotFound         Code = "NOT_FOUND"
	CodeResourceNotFound Code = "RESOURCE_NOT_FOUND"
	CodeConflict         Code = "CONFLICT"

	CodeRateLimited       Code = "RATE_LIMITED"
	CodeRateLimitExceeded Code = "RATE_LIMIT_EXCEEDED"
	CodeQuotaExceeded     Code = "QUOTA_EXCEEDED"

	CodeTimeout      Code = "TIMEOUT"
	CodeNetworkError Code = "NETWORK_ERROR"

	CodeBrowserError         Code = "BROWSER_ERROR"
	CodeBrowserTimeout       Code = "BROWSER_TIMEOUT"
	CodeBrowserUnavailable   Code = "BROWSER_UNAVAILABLE"
	CodeBrowserLaunchFailed  Code = "BROWSER_LAUNCH_FAILED"
	CodeBrowserContextFailed Code = "BROWSER_CONTEXT_FAILED"
	CodeBrowserPageFailed    Code = "BROWSER_PAGE_FAILED"

	CodeProviderError           Code = "PROVIDER_ERROR"
	CodeProviderTimeout         Code = "PROVIDER_TIMEOUT"
	CodeProviderUnavailable     Code = "PROVIDER_UNAVAILABLE"
	CodeProviderInvalidResponse Code = "PROVIDER_INVALID_RESPONSE"

	CodeDownloaderError     Code = "DOWNLOADER_ERROR"
	CodeDownloadFailed      Code = "DOWNLOAD_FAILED"
	CodeDownloadUnavailable Code = "DOWNLOAD_UNAVAILABLE"
	CodeMediaNotFound       Code = "MEDIA_NOT_FOUND"

	CodeInternalError Code = "INTERNAL_ERROR"

	CodeNotImplemented Code = "NOT_IMPLEMENTED"
)
