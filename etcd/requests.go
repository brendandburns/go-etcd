package etcd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strings"
	"time"
)

// Valid options for GET, PUT, POST, DELETE
// Using CAPITALIZED_UNDERSCORE to emphasize that these
// values are meant to be used as constants.
var (
	VALID_GET_OPTIONS = validOptions{
		"recursive":  reflect.Bool,
		"consistent": reflect.Bool,
		"sorted":     reflect.Bool,
		"wait":       reflect.Bool,
		"waitIndex":  reflect.Uint64,
	}

	VALID_PUT_OPTIONS = validOptions{
		"prevValue": reflect.String,
		"prevIndex": reflect.Uint64,
		"prevExist": reflect.Bool,
	}

	VALID_POST_OPTIONS = validOptions{}

	VALID_DELETE_OPTIONS = validOptions{
		"recursive": reflect.Bool,
	}
)

// get issues a GET request
func (c *Client) get(key string, options options) (*Response, error) {
	logger.Debugf("get %s [%s]", key, c.cluster.Leader)

	p := path.Join("keys", key)
	if options != nil {
		str, err := optionsToString(options, VALID_GET_OPTIONS)
		if err != nil {
			return nil, err
		}
		p += str
	}

	resp, err := c.sendRequest("GET", p, "")

	if err != nil {
		return nil, err
	}

	return resp, nil
}

// put issues a PUT request
func (c *Client) put(key string, value string, ttl uint64, options options) (*Response, error) {
	logger.Debugf("put %s, %s, ttl: %d, [%s]", key, value, ttl, c.cluster.Leader)
	v := url.Values{}

	if value != "" {
		v.Set("value", value)
	}

	if ttl > 0 {
		v.Set("ttl", fmt.Sprintf("%v", ttl))
	}

	p := path.Join("keys", key)
	if options != nil {
		str, err := optionsToString(options, VALID_PUT_OPTIONS)
		if err != nil {
			return nil, err
		}
		p += str
	}

	resp, err := c.sendRequest("PUT", p, v.Encode())

	if err != nil {
		return nil, err
	}

	return resp, nil
}

// post issues a POST request
func (c *Client) post(key string, value string, ttl uint64) (*Response, error) {
	logger.Debugf("post %s, %s, ttl: %d, [%s]", key, value, ttl, c.cluster.Leader)
	v := url.Values{}

	if value != "" {
		v.Set("value", value)
	}

	if ttl > 0 {
		v.Set("ttl", fmt.Sprintf("%v", ttl))
	}

	resp, err := c.sendRequest("POST", path.Join("keys", key), v.Encode())

	if err != nil {
		return nil, err
	}

	return resp, nil
}

// delete issues a DELETE request
func (c *Client) delete(key string, options options) (*Response, error) {
	logger.Debugf("delete %s [%s]", key, c.cluster.Leader)
	v := url.Values{}

	p := path.Join("keys", key)
	if options != nil {
		str, err := optionsToString(options, VALID_DELETE_OPTIONS)
		if err != nil {
			return nil, err
		}
		p += str
	}

	resp, err := c.sendRequest("DELETE", p, v.Encode())

	if err != nil {
		return nil, err
	}

	return resp, nil
}

// sendRequest sends a HTTP request and returns a Response as defined by etcd
func (c *Client) sendRequest(method string, _path string, body string) (*Response, error) {

	var resp *http.Response
	var req *http.Request

	retry := 0
	// if we connect to a follower, we will retry until we found a leader
	for {
		var httpPath string

		// If _path has schema already, then it's assumed to be
		// a complete URL and therefore needs no further processing.
		u, err := url.Parse(_path)
		if err != nil {
			return nil, err
		}

		if u.Scheme != "" {
			httpPath = _path
		} else {
			httpPath = c.getHttpPath(_path)
		}

		logger.Debug("send.request.to ", httpPath, " | method ", method)
		if body == "" {

			req, _ = http.NewRequest(method, httpPath, nil)

		} else {
			req, _ = http.NewRequest(method, httpPath, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; param=value")
		}

		resp, err = c.httpClient.Do(req)

		logger.Debug("recv.response.from ", httpPath)
		// network error, change a machine!
		if err != nil {
			retry++
			if retry > 2*len(c.cluster.Machines) {
				return nil, errors.New("Cannot reach servers")
			}
			num := retry % len(c.cluster.Machines)
			logger.Debug("update.leader[", c.cluster.Leader, ",", c.cluster.Machines[num], "]")
			c.cluster.Leader = c.cluster.Machines[num]
			time.Sleep(time.Millisecond * 200)
			continue
		}

		if resp != nil {
			if resp.StatusCode == http.StatusTemporaryRedirect {
				httpPath := resp.Header.Get("Location")

				resp.Body.Close()

				if httpPath == "" {
					return nil, errors.New("Cannot get redirection location")
				}

				c.updateLeader(httpPath)
				logger.Debug("send.redirect")
				// try to connect the leader
				continue
			} else if resp.StatusCode == http.StatusInternalServerError {
				resp.Body.Close()

				retry++
				if retry > 2*len(c.cluster.Machines) {
					return nil, errors.New("Cannot reach servers")
				}
				continue
			} else {
				logger.Debug("send.return.response ", httpPath)
				break
			}

		}
		logger.Debug("error.from ", httpPath, " ", err.Error())
		return nil, err
	}

	// Convert HTTP response to etcd response
	b, err := ioutil.ReadAll(resp.Body)

	resp.Body.Close()

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, handleError(b)
	}

	var result Response

	err = json.Unmarshal(b, &result)

	if err != nil {
		return nil, err
	}

	return &result, nil
}
