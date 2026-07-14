package dockerdeploy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/omry/reploy/internal/blueprint"
)

func WaitForHTTPReadiness(ctx context.Context, endpoint EndpointExecutionPlan) error {
	return WaitForHTTPReadinessWithServiceCheck(ctx, endpoint, nil)
}

func WaitForHTTPReadinessWithServiceCheck(ctx context.Context, endpoint EndpointExecutionPlan, serviceCheck func(context.Context) error) error {
	if endpoint.Readiness == nil {
		return fmt.Errorf("endpoint has no readiness configuration")
	}
	readiness := endpoint.Readiness
	target := readinessTarget(endpoint)
	client := readinessHTTPClient(readiness)
	defer client.Transport.(*http.Transport).CloseIdleConnections()
	return waitForHTTPReadinessWithServiceCheck(ctx, endpoint, target, client, serviceCheck)
}

func readinessTarget(endpoint EndpointExecutionPlan) string {
	hostPort := net.JoinHostPort(endpoint.ProbeHost, fmt.Sprintf("%d", endpoint.PublishedPort))
	return (&url.URL{Scheme: endpoint.Scheme, Host: hostPort, Path: endpoint.Readiness.Path}).String()
}

func readinessHTTPClient(readiness *blueprint.Readiness) *http.Client {
	return &http.Client{
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: !readiness.TLSVerify}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func waitForHTTPReadiness(ctx context.Context, endpoint EndpointExecutionPlan, target string, client *http.Client) error {
	return waitForHTTPReadinessWithServiceCheck(ctx, endpoint, target, client, nil)
}

func waitForHTTPReadinessWithServiceCheck(ctx context.Context, endpoint EndpointExecutionPlan, target string, client *http.Client, serviceCheck func(context.Context) error) error {
	readiness := endpoint.Readiness
	probeCtx, cancel := context.WithTimeout(ctx, readiness.Timeout)
	defer cancel()
	started := time.Now()
	lastError := error(nil)
	for {
		if serviceCheck != nil {
			if err := serviceCheck(probeCtx); err != nil {
				return fmt.Errorf("readiness probe %s stopped because the workload left running state: %w", target, err)
			}
		}
		request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
			lastError = fmt.Errorf("HTTP status %d", response.StatusCode)
		} else {
			lastError = err
		}
		select {
		case <-probeCtx.Done():
			return fmt.Errorf("readiness probe %s failed after %s; last error: %v", target, time.Since(started).Round(time.Millisecond), lastError)
		case <-time.After(readiness.Interval):
		}
	}
}
