package httpx

import "net/http"

const UserAgent = "emos-forge-worker"

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type UserAgentDoer struct {
	Next Doer
}

func (d UserAgentDoer) Do(request *http.Request) (*http.Response, error) {
	SetUserAgent(request)
	return d.Next.Do(request)
}

func SetUserAgent(request *http.Request) {
	request.Header.Set("User-Agent", UserAgent)
}
