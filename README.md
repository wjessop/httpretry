# A re-tryable HTTP client for Go

Safely re-try failing requests in Go with backoff.

## Usage

### A simple usage with some sensible defaults

```go
client := hr.NewClient()
```

You can configure the library:

```go
client.RetryMax = 100
client.RetryWaitMax = 60 * time.Second
```

You can also specify your own retry policy. By default only 503 errors are retried as per the RFCs:

```go
// Retry on 500 errors, not 503, but also retry on 401
client.CheckForRetry = func(resp *http.Response, err error) (bool, error) {
	if err != nil {
		return true, err
	}
	if resp.StatusCode == 0 || resp.StatusCode >= 500 || resp.StatusCode == 401 {
		return true, nil
	}

	return false, nil
}
```

### Calling underlying http.Client methods

The library wraps the standard HTTP client so you can call the underlying methods.

**Adding a cookie jar**

```go
client := hr.NewClient()

cookieJar, err := cookiejar.New(nil)
if err != nil {
	panic(err)
}
client.HTTPClient.Jar = cookieJar
```

**Setting headers and the content length**

```go
req, err := httpretry.NewRequestWithContext(ctx, "POST", url, data)
if err != nil {
	return nil, err
}
req.Header.Add("Content-Type", "application/json; charset=utf-8")
req.Header.Add("Accept", "application/json")
req.ContentLength = int64(len(data))
```

## TODO

Handle context better in the client.Do retry loop

## Ackbowledgements

This repo was based on, and inspired by [this code](https://medium.com/@nitishkr88/http-retries-in-go-e622e51d249f), with a few changes and fixes.