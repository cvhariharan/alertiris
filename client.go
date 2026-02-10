package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type IRISClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type IRISAlertRequest struct {
	Title            string `json:"alert_title"`
	Description      string `json:"alert_description,omitempty"`
	Source           string `json:"alert_source,omitempty"`
	SourceRef        string `json:"alert_source_ref,omitempty"`
	SourceLink       string `json:"alert_source_link,omitempty"`
	SourceEventTime  string `json:"alert_source_event_time,omitempty"`
	SourceContent    any    `json:"alert_source_content,omitempty"`
	SeverityID       int    `json:"alert_severity_id"`
	StatusID         int    `json:"alert_status_id"`
	CustomerID       int    `json:"alert_customer_id"`
	ClassificationID int    `json:"alert_classification_id,omitempty"`
	Note             string `json:"alert_note"`
	Tags             string `json:"alert_tags,omitempty"`
}

type IRISAlertUpdateRequest struct {
	Title            *string `json:"alert_title,omitempty"`
	Description      *string `json:"alert_description,omitempty"`
	Source           *string `json:"alert_source,omitempty"`
	SourceRef        *string `json:"alert_source_ref,omitempty"`
	SourceLink       *string `json:"alert_source_link,omitempty"`
	SourceEventTime  *string `json:"alert_source_event_time,omitempty"`
	SourceContent    any     `json:"alert_source_content,omitempty"`
	SeverityID       *int    `json:"alert_severity_id,omitempty"`
	StatusID         *int    `json:"alert_status_id,omitempty"`
	CustomerID       *int    `json:"alert_customer_id,omitempty"`
	ClassificationID *int    `json:"alert_classification_id,omitempty"`
	Tags             *string `json:"alert_tags,omitempty"`
}

type IRISResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Msg    string          `json:"message"`
}

type IRISAlertData struct {
	AlertID int `json:"alert_id"`
}

func NewIRISClient(cfg IRISConfig) *IRISClient {
	transport := &http.Transport{}
	if cfg.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &IRISClient{
		baseURL: strings.TrimRight(cfg.URL, "/"),
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Transport: transport,
		},
	}
}

func (c *IRISClient) CreateAlert(req IRISAlertRequest, cid int) (int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return 0, fmt.Errorf("marshal create request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/alerts/add", body, cid)
	if err != nil {
		return 0, err
	}

	var data IRISAlertData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return 0, fmt.Errorf("unmarshal alert data: %w", err)
	}
	return data.AlertID, nil
}

func (c *IRISClient) UpdateAlert(alertID int, req IRISAlertUpdateRequest, cid int) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal update request: %w", err)
	}

	_, err = c.do(http.MethodPost, fmt.Sprintf("/alerts/update/%d", alertID), body, cid)
	return err
}

func (c *IRISClient) DeleteAlert(alertID int, cid int) error {
	_, err := c.do(http.MethodPost, fmt.Sprintf("/alerts/delete/%d", alertID), nil, cid)
	return err
}

func (c *IRISClient) do(method, path string, body []byte, cid int) (*IRISResponse, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	url := fmt.Sprintf("%s%s?cid=%d", c.baseURL, path, cid)
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("iris api %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	var irisResp IRISResponse
	if err := json.Unmarshal(respBody, &irisResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if irisResp.Status != "success" {
		return nil, fmt.Errorf("iris api error: %s", irisResp.Msg)
	}

	return &irisResp, nil
}
