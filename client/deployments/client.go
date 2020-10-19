// Copyright 2020 Northern.tech AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.
package deployments

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/pkg/errors"

	"github.com/mendersoftware/mender-cli/log"
)

type artifactsList struct {
	artifacts []artifactData
}

type artifactData struct {
	ID                    string   `json:"id"`
	Description           string   `json:"description"`
	Name                  string   `json:"name"`
	DeviceTypesCompatible []string `json:"device_types_compatible"`
	Info                  struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	} `json:"info"`
	Signed  bool `json:"signed"`
	Updates []struct {
		TypeInfo struct {
			Type string `json:"type"`
		} `json:"type_info"`
		Files []struct {
			Name     string    `json:"name"`
			Checksum string    `json:"checksum"`
			Size     int       `json:"size"`
			Date     time.Time `json:"date"`
		} `json:"files"`
		MetaData []interface{} `json:"meta_data"`
	} `json:"updates"`
	ArtifactProvides struct {
		ArtifactName string `json:"artifact_name"`
	} `json:"artifact_provides"`
	ArtifactDepends struct {
		DeviceType []string `json:"device_type"`
	} `json:"artifact_depends"`
	Size     int       `json:"size"`
	Modified time.Time `json:"modified"`
}

const (
	artifactUploadURL = "/api/management/v1/deployments/artifacts"
	artifactsListURL  = artifactUploadURL
)

type Client struct {
	url               string
	artifactUploadURL string
	artifactsListURL  string
	client            *http.Client
}

func NewClient(url string, skipVerify bool) *Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify},
	}

	return &Client{
		url:               url,
		artifactUploadURL: JoinURL(url, artifactUploadURL),
		artifactsListURL:  JoinURL(url, artifactsListURL),
		client: &http.Client{
			Transport: tr,
		},
	}
}

func (c *Client) ListArtifacts(tokenPath string, detailLevel int) error {
	if detailLevel > 3 || detailLevel < 0 {
		return fmt.Errorf("FAILURE: invalid artifact detail")
	}
	token, err := ioutil.ReadFile(tokenPath)
	if err != nil {
		return errors.Wrap(err, "Please Login first")
	}

	req, err := http.NewRequest(http.MethodGet, c.artifactsListURL, nil)
	if err != nil {
		return errors.Wrap(err, "Failed to create HTTP request")
	}
	req.Header.Set("Authorization", "Bearer "+string(token))

	reqDump, err := httputil.DumpRequest(req, false)
	if err != nil {
		return err
	}
	log.Verbf("sending request: \n%s", string(reqDump))

	rsp, err := c.client.Do(req)
	if err != nil {
		return errors.Wrap(err, "Get /deployments/artifacts request failed")
	}
	if rsp.StatusCode != 200 {
		return fmt.Errorf("Get /deployments/artifacts request failed with status %d\n", rsp.StatusCode)
	}

	defer rsp.Body.Close()

	body, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return err
	}

	var list artifactsList
	err = json.Unmarshal(body, &list.artifacts)
	if err != nil {
		return err
	}
	for _, v := range list.artifacts {
		listArtifact(v, detailLevel)
	}

	return nil
}

func listArtifact(a artifactData, detailLevel int) {
	fmt.Printf("ID: %s\n", a.ID)
	fmt.Printf("Name: %s\n", a.Name)
	if detailLevel >= 1 {
		fmt.Printf("Signed: %t\n", a.Signed)
		fmt.Printf("Modfied: %s\n", a.Modified)
		fmt.Printf("Size: %d\n", a.Size)
		fmt.Printf("Description: %s\n", a.Description)
		fmt.Println("Compatible device types:")
		for _, v := range a.DeviceTypesCompatible {
			fmt.Printf("  %s\n", v)
		}
		fmt.Printf("Artifact format: %s\n", a.Info.Format)
		fmt.Printf("Format version: %d\n", a.Info.Version)
	}
	if detailLevel >= 2 {
		fmt.Printf("Artifact provides: %s\n", a.ArtifactProvides.ArtifactName)
		fmt.Println("Artifact depends:")
		for _, v := range a.ArtifactDepends.DeviceType {
			fmt.Printf("  %s\n", v)
		}
		fmt.Println("Updates:")
		for _, v := range a.Updates {
			fmt.Printf("  Type: %s\n", v.TypeInfo.Type)
			fmt.Println("  Files:")
			for _, f := range v.Files {
				fmt.Printf("\tName: %s\n", f.Name)
				fmt.Printf("\tChecksum: %s\n", f.Checksum)
				fmt.Printf("\tSize: %d\n", f.Size)
				fmt.Printf("\tDate: %s\n", f.Date)
				if len(v.Files) > 1 {
					fmt.Println()
				}
			}
			if detailLevel == 3 {
				fmt.Printf("  MetaData: %v\n", v.MetaData)
			}
		}
	}

	fmt.Println("--------------------------------------------------------------------------------")
}

func (c *Client) UploadArtifact(description, artifactPath, tokenPath string, noProgress bool) error {
	var bar *pb.ProgressBar

	artifact, err := os.Open(artifactPath)
	if err != nil {
		return errors.Wrap(err, "Cannot read artifact file")
	}

	artifactStats, err := artifact.Stat()
	if err != nil {
		return errors.Wrap(err, "Cannot read artifact file stats")
	}

	// create pipe
	pR, pW := io.Pipe()

	// create multipart writer
	writer := multipart.NewWriter(pW)

	token, err := ioutil.ReadFile(tokenPath)
	if err != nil {
		return errors.Wrap(err, "Please Login first")
	}

	req, err := http.NewRequest(http.MethodPost, c.artifactUploadURL, pR)
	if err != nil {
		return errors.Wrap(err, "Cannot create request")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+string(token))

	reqDump, _ := httputil.DumpRequest(req, false)
	log.Verbf("sending request: \n%v", string(reqDump))

	if !noProgress {
		// create progress bar
		bar = pb.New64(artifactStats.Size()).Set(pb.Bytes, true).SetRefreshRate(time.Millisecond * 100)
		bar.Start()
	}

	go func() {
		var part io.Writer
		defer pW.Close()
		defer artifact.Close()

		writer.WriteField("size", strconv.FormatInt(artifactStats.Size(), 10))
		writer.WriteField("description", description)
		part, _ = writer.CreateFormFile("artifact", artifactStats.Name())

		if !noProgress {
			part = bar.NewProxyWriter(part)
		}

		if _, err := io.Copy(part, artifact); err != nil {
			writer.Close()
			pR.CloseWithError(err)
			return
		}

		writer.Close()
		if !noProgress {
			log.Info("Processing uploaded file. This may take around one minute.\n")
		}
		return
	}()

	rsp, err := c.client.Do(req)
	if err != nil {
		return errors.Wrap(err, "POST /artifacts request failed")
	}
	defer rsp.Body.Close()
	pR.Close()

	rspDump, _ := httputil.DumpResponse(rsp, true)
	log.Verbf("response: \n%v\n", string(rspDump))

	if rsp.StatusCode != http.StatusCreated {
		body, err := ioutil.ReadAll(rsp.Body)
		if err != nil {
			return errors.Wrap(err, "can't read request body")
		}
		if rsp.StatusCode == http.StatusUnauthorized {
			log.Verbf("artifact upload failed with status %d, reason: %s", rsp.StatusCode, body)
			return errors.New("Unauthorized. Please Login first")
		}
		return errors.New(fmt.Sprintf("artifact upload failed with status %d, reason: %s", rsp.StatusCode, body))
	}

	return nil
}

func JoinURL(base, url string) string {
	if strings.HasPrefix(url, "/") {
		url = url[1:]
	}
	if !strings.HasSuffix(base, "/") {
		base = base + "/"
	}
	return base + url
}
