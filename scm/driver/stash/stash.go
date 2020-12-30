// Copyright 2017 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package stash implements a Bitbucket Server client.
package stash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"strings"

	"github.com/blang/semver"
	"github.com/drone/go-scm/scm"
	"github.com/drone/go-scm/scm/driver/internal/null"
)

// Reference API Documentation:
//   https://docs.atlassian.com/bitbucket-server/rest/5.11.1/bitbucket-rest.html

// New returns a new Stash API client.
func New(uri string) (*scm.Client, error) {
	return NewVersioned(uri, "")
}

// NewVersioned returns a new versioned Stash API client.
func NewVersioned(uri string, version string) (*scm.Client, error) {
	base, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path = base.Path + "/"
	}

	var stashVersion *semver.Version
	if len(version) > 0 {
		stashVersion, err = semver.New(version)
		if err != nil {
			return nil, err
		}
	}

	client := &wrapper{new(scm.Client), stashVersion}
	client.BaseURL = base
	// initialize services
	client.Driver = scm.DriverStash
	client.Linker = &linker{base.String()}
	client.Contents = &contentService{client}
	client.Git = &gitService{client}
	client.Issues = &issueService{client}
	client.Organizations = &organizationService{client}
	client.PullRequests = &pullService{client}
	client.Repositories = &repositoryService{client}
	client.Reviews = &reviewService{client}
	client.Users = &userService{client}
	client.Webhooks = &webhookService{client}
	return client.Client, nil
}

// NewDefault returns a new Stash API client.
func NewDefault() *scm.Client {
	client, _ := New("http://localhost:7990")
	return client
}

// wraper wraps the Client to provide high level helper functions
// for making http requests and unmarshaling the response.
type wrapper struct {
	*scm.Client
	version *semver.Version
}

// do wraps the Client.Do function by creating the Request and
// unmarshalling the response.
func (c *wrapper) do(ctx context.Context, method, path string, in, out interface{}) (*scm.Response, error) {
	req := &scm.Request{
		Method: method,
		Path:   path,
		Header: map[string][]string{
			"Accept": {"application/json"},
		},
	}
	println(fmt.Sprintf("Connecting to Bitbucket: %s %s", method, path))
	// if we are posting or putting data, we need to
	// write it to the body of the request.
	if in != nil {
		buf := new(bytes.Buffer)
		json.NewEncoder(buf).Encode(in)
		req.Header["Content-Type"] = []string{"application/json"}
		req.Body = buf
		println(fmt.Sprintf("Request Body: %s", buf.String()))
	}

	// execute the http request
	res, err := c.Client.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	// if an error is encountered, unmarshal and return the
	// error response.
	if res.Status == 401 {
		return res, scm.ErrNotAuthorized
	} else if res.Status > 300 {
		err := new(Error)
		println(fmt.Sprintf("Error connection to stash / bitbucket Status: %d", res.Status))
		body, e := ioutil.ReadAll(res.Body)
		if e != nil {
			println("Error reading body")
		} else {
			println(string(body))
		}

		_ = json.Unmarshal(body, err)
		return res, err
	}

	if out == nil {
		return res, nil
	}

	// if raw output is expected, copy to the provided
	// buffer and exit.
	if w, ok := out.(io.Writer); ok {
		io.Copy(w, res.Body)
		return res, nil
	}

	// if a json response is expected, parse and return
	// the json response.
	return res, json.NewDecoder(res.Body).Decode(out)
}

// pagination represents Bitbucket pagination properties
// embedded in list responses.
type pagination struct {
	Start    null.Int  `json:"start"`
	Size     null.Int  `json:"size"`
	Limit    null.Int  `json:"limit"`
	LastPage null.Bool `json:"isLastPage"`
	NextPage null.Int  `json:"nextPageStart"`
}

// Error represents a Stash error.
type Error struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
	Errors  []struct {
		Message         string `json:"message"`
		ExceptionName   string `json:"exceptionName"`
		CurrentVersion  int    `json:"currentVersion"`
		ExpectedVersion int    `json:"expectedVersion"`
	} `json:"errors"`
}

func (e *Error) Error() string {
	if len(e.Errors) == 0 {
		if len(e.Message) > 0 {
			return fmt.Sprintf("bitbucket: status: %d message: %s", e.Status, e.Message)
		}
		return "bitbucket: undefined error"
	}
	return e.Errors[0].Message
}
