package errors

import "net/http"

func New(status int, code Code, category Category, message string) *AppError {
	return &AppError{
		Status:   status,
		Code:     code,
		Category: category,
		Message:  message,
	}
}

func Wrap(cause error, status int, code Code, category Category, message string) *AppError {
	return New(status, code, category, message).WithCause(cause)
}

func Validation(code Code, message string) *AppError {
	return New(http.StatusBadRequest, code, CategoryValidation, message)
}

func BadRequest(code Code, message string) *AppError {
	return New(http.StatusBadRequest, code, CategoryBadRequest, message)
}

func Authentication(code Code, message string) *AppError {
	return New(http.StatusUnauthorized, code, CategoryAuthentication, message)
}

func Authorization(code Code, message string) *AppError {
	return New(http.StatusForbidden, code, CategoryAuthorization, message)
}

func NotFound(code Code, message string) *AppError {
	return New(http.StatusNotFound, code, CategoryNotFound, message)
}

func Conflict(code Code, message string) *AppError {
	return New(http.StatusConflict, code, CategoryConflict, message)
}

func RateLimit(code Code, message string) *AppError {
	return New(http.StatusTooManyRequests, code, CategoryRateLimit, message).WithRetryable(true)
}

func Timeout(code Code, message string) *AppError {
	return New(http.StatusGatewayTimeout, code, CategoryTimeout, message).WithRetryable(true)
}

func Network(code Code, message string) *AppError {
	return New(http.StatusBadGateway, code, CategoryNetwork, message).WithRetryable(true)
}

func Browser(code Code, message string) *AppError {
	return New(http.StatusInternalServerError, code, CategoryBrowser, message)
}

func Provider(code Code, message string) *AppError {
	return New(http.StatusBadGateway, code, CategoryProvider, message)
}

func Downloader(code Code, message string) *AppError {
	return New(http.StatusInternalServerError, code, CategoryDownloader, message)
}

func Internal(code Code, message string) *AppError {
	return New(http.StatusInternalServerError, code, CategoryInternal, message)
}

func UnsupportedPlatform(code Code, message string) *AppError {
	return New(http.StatusBadRequest, code, CategoryUnsupportedPlatform, message)
}

func UnavailableService(code Code, message string) *AppError {
	return New(http.StatusServiceUnavailable, code, CategoryUnavailableService, message).WithRetryable(true)
}

func NotImplemented(code Code, message string) *AppError {
	return New(http.StatusNotImplemented, code, CategoryInternal, message)
}

func ProviderUnavailable(message string) *AppError {
	return New(http.StatusServiceUnavailable, CodeProviderUnavailable, CategoryProvider, message).WithRetryable(true)
}

func ProviderTimeout(message string) *AppError {
	return New(http.StatusGatewayTimeout, CodeProviderTimeout, CategoryProvider, message).WithRetryable(true)
}

func ProviderInvalidResponse(message string) *AppError {
	return New(http.StatusBadGateway, CodeProviderInvalidResponse, CategoryProvider, message)
}

func MediaNotFound(message string) *AppError {
	return New(http.StatusNotFound, CodeMediaNotFound, CategoryDownloader, message)
}

func BrowserTimeout(message string) *AppError {
	return New(http.StatusGatewayTimeout, CodeBrowserTimeout, CategoryBrowser, message).WithRetryable(true)
}

func BrowserUnavailable(message string) *AppError {
	return New(http.StatusServiceUnavailable, CodeBrowserUnavailable, CategoryBrowser, message).WithRetryable(true)
}

func NetworkError(message string) *AppError {
	return Network(CodeNetworkError, message)
}

func TimeoutError(message string) *AppError {
	return Timeout(CodeTimeout, message)
}

func RateLimited(message string) *AppError {
	return RateLimit(CodeRateLimited, message)
}

func APIKeyMissing(message string) *AppError {
	return Authentication(CodeAPIKeyMissing, message)
}

func APIKeyInvalid(message string) *AppError {
	return Authentication(CodeAPIKeyInvalid, message)
}

func APIKeyExpired(message string) *AppError {
	return Authentication(CodeAPIKeyExpired, message)
}

func APIKeyRevoked(message string) *AppError {
	return Authentication(CodeAPIKeyRevoked, message)
}

func AccountDisabled(message string) *AppError {
	return Authorization(CodeAccountDisabled, message)
}

func AccountSuspended(message string) *AppError {
	return Authorization(CodeAccountSuspended, message)
}

func RateLimitExceeded(message string) *AppError {
	return RateLimit(CodeRateLimitExceeded, message)
}

func QuotaExceeded(message string) *AppError {
	return New(http.StatusTooManyRequests, CodeQuotaExceeded, CategoryRateLimit, message)
}
