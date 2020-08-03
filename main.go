package httpretry

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"time"

	"github.com/hashicorp/go-cleanhttp"
)

// CheckForRetry specifies a policy for handling retries. It is called
// following each request with the reponse and error values returned by
// the http.Client. If it returns false, the Client stops retrying
// and returns the response to the caller. If it returns an error,
// that error value is return in lieu of the error from the request.
type CheckForRetry func(resp *http.Response, err error) (bool, error)

// DefaultRetryPolicy provides a default callback for Client.CheckForRetry,
// which will retry on connection errors and server errors.
func DefaultRetryPolicy(resp *http.Response, err error) (bool, error) {
	if err != nil {
		return true, err
	}
	// Check the response code. Here we retry on 500-range responses to
	// allow the server time to recover.
	if resp.StatusCode == 0 || resp.StatusCode == 503 {
		return true, nil
	}

	return false, nil
}

// Backoff specifies a policy for how long to wait between retries.
// It is called after a failing request to determine the amount of time
// that should pass before trying again.
type Backoff func(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration

// DefaultBackoff provides a default callback for Client.Backoff which
// will perform exponential backoff based on the attempt number and limited
// by the provided minimum and maximum durations.
func DefaultBackoff(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration {
	mult := math.Pow(2, float64(attemptNum)) * float64(min)
	sleep := time.Duration(mult)
	if float64(sleep) != mult || sleep > max {
		sleep = max
	}
	return sleep
}

var (
	// Default retry configuration
	defaultRetryWaitMin = 1 * time.Second
	defaultRetryWaitMax = 30 * time.Second
	defaultRetryMax     = 4
)

// Client is used to make TTP requests. It adds additional functionality
// like automatic retries to tolerate minor outages.
type Client struct {
	HTTPClient   *http.Client  // Internal HTTP client
	RetryWaitMin time.Duration // Minimum time to wait
	RetryWaitMax time.Duration // Maximum time to wait
	RetryMax     int           // Maximum number of retries

	// CheckForRetry specifies the policy for handling retries, and is called
	// after each request.
	CheckForRetry CheckForRetry

	// Backoff specifies the policy for how long to wait between retries
	Backoff Backoff
}

// NewClient creates a new client with default settings
func NewClient() *Client {
	return &Client{
		HTTPClient:    cleanhttp.DefaultClient(),
		RetryWaitMin:  defaultRetryWaitMin,
		RetryWaitMax:  defaultRetryWaitMax,
		RetryMax:      defaultRetryMax,
		CheckForRetry: DefaultRetryPolicy,
		Backoff:       DefaultBackoff,
	}
}

// Request wraps the metadata needed to create HTTP requests
type Request struct {
	// body is a seekable rader over the request body payload. This is
	// used to rewind the request data in between retries.
	body io.ReadSeeker

	// Embed an HTTP request directly. This makes a *Request act exacty
	// like an *http.Request so that all meta methods are supported.
	*http.Request
}

// NewRequestWithContext creates a new wrapped request
func NewRequestWithContext(ctx context.Context, method, url string, body io.ReadSeeker) (*Request, error) {
	// Wrap the body in a noop ReadCloser if non-nil. This prevents the
	// reader from being closed by the HTTP client.
	var rcBody io.ReadCloser
	if body != nil {
		rcBody = ioutil.NopCloser(body)
	}

	// Make the request with the noop-closer for the body
	httpReq, err := http.NewRequestWithContext(ctx, method, url, rcBody)
	if err != nil {
		return nil, err
	}

	return &Request{body, httpReq}, nil
}

// Do wraps calling an HTTP method with retries
func (c *Client) Do(req *Request) (*http.Response, error) {
	for i := 0; i < c.RetryMax; i++ {
		var code int // HTTP response code

		// Always rewind the request body when non-nil
		if req.body != nil {
			if _, err := req.body.Seek(0, 0); err != nil {
				return nil, fmt.Errorf("failed to seek body: %v", err)
			}
		}

		// Attempt the request
		resp, err := c.HTTPClient.Do(req.Request)

		// Check if we should continue with retries
		checkOK, checkErr := c.CheckForRetry(resp, err)

		if err != nil {
			fmt.Printf("[ERROR] %s %s request failed: %v\n", req.Method, req.URL, err)
		} else {
			// Call this here to maintain the behaviour of logging all requests, etc
			// even if CheckForRetry signals to stop.
		}

		// Now decide if we should continue
		if !checkOK {
			if checkErr != nil {
				err = checkErr
			}
			return resp, err
		}

		// We're going to retry, consume any response to re-use the connection
		if err == nil {
			c.drainBody(resp.Body)
		}

		remain := c.RetryMax - i
		if remain == 0 {
			break
		}
		wait := c.Backoff(c.RetryWaitMin, c.RetryWaitMax, i, resp)
		desc := fmt.Sprintf("%s %s", req.Method, req.URL)
		if code > 0 {
			desc = fmt.Sprintf("%s (status: %d)", desc, code)
		}
		fmt.Printf("[DEBUG] %s: retrying in %s (%d left)\n", desc, wait, remain)
		time.Sleep(wait)
	}

	// Return an error if we fall out of the retry loop
	return nil, fmt.Errorf("%s %s giving up after %d attempts", req.Method, req.URL, c.RetryMax+1)
}

// Try to read the response body so we can reuse this connection
func (c *Client) drainBody(body io.ReadCloser) {
	defer body.Close()
	_, err := io.Copy(ioutil.Discard, body)
	if err != nil {
		fmt.Printf("[ERROR] error reading response body: %v", err)
	}
}

// Get is a convenience helper for doing simple GET requests
func (c *Client) Get(url string) (*http.Response, error) {
	req, err := NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Post is a convenience helper for doing simple POST requests
func (c *Client) Post(url, bodyType string, body io.ReadSeeker) (*http.Response, error) {
	req, err := NewRequestWithContext(context.Background(), "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", bodyType)
	return c.Do(req)
}
