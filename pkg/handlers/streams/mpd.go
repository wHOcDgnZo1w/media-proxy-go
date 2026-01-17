package streams

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"media-proxy-go/pkg/httpclient"
	"media-proxy-go/pkg/interfaces"
	"media-proxy-go/pkg/logging"
	"media-proxy-go/pkg/types"
)

// MPDHandler processes DASH/MPD streams using FFmpeg transcoding.
type MPDHandler struct {
	client     *httpclient.Client
	log        *logging.Logger
	baseURL    string
	transcoder interfaces.Transcoder
}

// NewMPDHandler creates a new MPD stream handler.
func NewMPDHandler(client *httpclient.Client, log *logging.Logger, baseURL string, transcoder interfaces.Transcoder) *MPDHandler {
	return &MPDHandler{
		client:     client,
		log:        log.WithComponent("mpd-handler"),
		baseURL:    baseURL,
		transcoder: transcoder,
	}
}

// Type returns the stream type.
func (h *MPDHandler) Type() types.StreamType {
	return types.StreamTypeMPD
}

// CanHandle returns true if the URL appears to be a DASH stream.
func (h *MPDHandler) CanHandle(urlStr string) bool {
	lower := strings.ToLower(urlStr)
	return strings.Contains(lower, ".mpd") ||
		strings.Contains(lower, "/dash/") ||
		strings.Contains(lower, "manifest(format=mpd")
}

// HandleManifest handles MPD manifests, transcoding to HLS via FFmpeg.
func (h *MPDHandler) HandleManifest(ctx context.Context, req *types.StreamRequest, baseURL string) (*types.StreamResponse, error) {
	h.log.Debug("handling MPD manifest", "url", req.URL)

	if h.transcoder != nil {
		return h.handleWithFFmpeg(ctx, req, baseURL)
	}

	// Fallback if transcoder unavailable
	return h.handleLegacy(ctx, req, baseURL)
}

// handleWithFFmpeg uses FFmpeg to transcode MPD to HLS.
func (h *MPDHandler) handleWithFFmpeg(ctx context.Context, req *types.StreamRequest, baseURL string) (*types.StreamResponse, error) {
	streamID, err := h.transcoder.StartStream(ctx, req.URL, req.Headers, req.ClearKey)
	if err != nil {
		return nil, fmt.Errorf("failed to start transcoding: %w", err)
	}

	// Build redirect URL to FFmpeg stream
	streamPath := h.transcoder.GetStreamPath(streamID)
	redirectURL := fmt.Sprintf("%s/ffmpeg_stream/%s/index.m3u8", baseURL, streamID)

	h.log.Debug("redirecting to FFmpeg stream", "stream_id", streamID, "path", streamPath)

	return &types.StreamResponse{
		StatusCode:  http.StatusFound,
		RedirectURL: redirectURL,
	}, nil
}

// handleLegacy rewrites MPD manifest and proxies through the proxy.
func (h *MPDHandler) handleLegacy(ctx context.Context, req *types.StreamRequest, baseURL string) (*types.StreamResponse, error) {
	// Fetch the original MPD manifest
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch MPD: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &types.StreamResponse{StatusCode: resp.StatusCode}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MPD: %w", err)
	}

	// Rewrite the MPD manifest
	rewritten, err := h.rewriteMPD(body, req.URL, baseURL, req.Headers, req.ClearKey)
	if err != nil {
		return nil, fmt.Errorf("failed to rewrite MPD: %w", err)
	}

	return &types.StreamResponse{
		ContentType: "application/dash+xml",
		Body:        io.NopCloser(bytes.NewReader(rewritten)),
		StatusCode:  http.StatusOK,
	}, nil
}

// HandleSegment proxies an MPD segment.
func (h *MPDHandler) HandleSegment(ctx context.Context, req *types.StreamRequest) (*types.StreamResponse, error) {
	h.log.Debug("handling MPD segment", "url", req.URL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch segment: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		if strings.HasSuffix(req.URL, ".m4s") {
			contentType = "video/iso.segment"
		} else {
			contentType = "application/octet-stream"
		}
	}

	return &types.StreamResponse{
		ContentType: contentType,
		Body:        resp.Body,
		StatusCode:  resp.StatusCode,
	}, nil
}

// rewriteMPD rewrites URLs in an MPD manifest.
func (h *MPDHandler) rewriteMPD(manifest []byte, originalURL, proxyBaseURL string, headers map[string]string, clearKey string) ([]byte, error) {
	baseURL, err := url.Parse(originalURL)
	if err != nil {
		return nil, err
	}

	// Parse as XML to rewrite URLs
	var mpd MPD
	if err := xml.Unmarshal(manifest, &mpd); err != nil {
		// If XML parsing fails, return original
		h.log.Warn("failed to parse MPD as XML, returning original", "error", err)
		return manifest, nil
	}

	// Rewrite BaseURL elements
	for i := range mpd.BaseURL {
		mpd.BaseURL[i] = h.buildProxyURL(h.resolveURL(mpd.BaseURL[i], baseURL), proxyBaseURL, headers)
	}

	// Rewrite URLs in Periods
	for pi := range mpd.Period {
		period := &mpd.Period[pi]

		for ai := range period.AdaptationSet {
			adaptSet := &period.AdaptationSet[ai]

			// Rewrite SegmentTemplate
			if adaptSet.SegmentTemplate != nil {
				h.rewriteSegmentTemplate(adaptSet.SegmentTemplate, baseURL, proxyBaseURL, headers)
			}

			// Rewrite Representations
			for ri := range adaptSet.Representation {
				rep := &adaptSet.Representation[ri]
				if rep.SegmentTemplate != nil {
					h.rewriteSegmentTemplate(rep.SegmentTemplate, baseURL, proxyBaseURL, headers)
				}
				if rep.BaseURL != "" {
					rep.BaseURL = h.buildProxyURL(h.resolveURL(rep.BaseURL, baseURL), proxyBaseURL, headers)
				}
			}
		}
	}

	// Add ClearKey ContentProtection if provided
	if clearKey != "" {
		h.addClearKeyProtection(&mpd, clearKey, proxyBaseURL)
	}

	return xml.MarshalIndent(mpd, "", "  ")
}

// rewriteSegmentTemplate rewrites URLs in a SegmentTemplate.
func (h *MPDHandler) rewriteSegmentTemplate(st *SegmentTemplate, baseURL *url.URL, proxyBaseURL string, headers map[string]string) {
	if st.Initialization != "" && !strings.Contains(st.Initialization, "$") {
		st.Initialization = h.buildProxyURL(h.resolveURL(st.Initialization, baseURL), proxyBaseURL, headers)
	}
	if st.Media != "" && !strings.Contains(st.Media, "$") {
		st.Media = h.buildProxyURL(h.resolveURL(st.Media, baseURL), proxyBaseURL, headers)
	}
}

// addClearKeyProtection adds ClearKey ContentProtection to the MPD.
func (h *MPDHandler) addClearKeyProtection(mpd *MPD, clearKey, proxyBaseURL string) {
	licenseURL := fmt.Sprintf("%s/license?clearkey=%s", proxyBaseURL, url.QueryEscape(clearKey))

	cp := ContentProtection{
		SchemeIdUri: "urn:uuid:e2719d58-a985-b3c9-781a-b030af78d30e",
		Value:       "ClearKey1.0",
		ClearKeyLicenseURL: &ClearKeyLicenseURL{
			LicenseType: "EME-1.0",
			URL:         licenseURL,
		},
	}

	// Add to all AdaptationSets
	for pi := range mpd.Period {
		for ai := range mpd.Period[pi].AdaptationSet {
			mpd.Period[pi].AdaptationSet[ai].ContentProtection = append(
				mpd.Period[pi].AdaptationSet[ai].ContentProtection, cp)
		}
	}
}

func (h *MPDHandler) resolveURL(urlStr string, base *url.URL) string {
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		return urlStr
	}
	ref, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}
	return base.ResolveReference(ref).String()
}

func (h *MPDHandler) buildProxyURL(targetURL, proxyBaseURL string, headers map[string]string) string {
	proxyURL, _ := url.Parse(proxyBaseURL + "/proxy/stream")
	query := proxyURL.Query()
	query.Set("url", targetURL)
	for key, value := range headers {
		query.Set("h_"+key, value)
	}
	proxyURL.RawQuery = query.Encode()
	return proxyURL.String()
}

// MPD XML structures
type MPD struct {
	XMLName xml.Name `xml:"MPD"`
	BaseURL []string `xml:"BaseURL,omitempty"`
	Period  []Period `xml:"Period"`
}

type Period struct {
	AdaptationSet []AdaptationSet `xml:"AdaptationSet"`
}

type AdaptationSet struct {
	ContentProtection []ContentProtection `xml:"ContentProtection,omitempty"`
	SegmentTemplate   *SegmentTemplate    `xml:"SegmentTemplate,omitempty"`
	Representation    []Representation    `xml:"Representation"`
}

type Representation struct {
	ID              string           `xml:"id,attr,omitempty"`
	BaseURL         string           `xml:"BaseURL,omitempty"`
	SegmentTemplate *SegmentTemplate `xml:"SegmentTemplate,omitempty"`
}

type SegmentTemplate struct {
	Initialization string `xml:"initialization,attr,omitempty"`
	Media          string `xml:"media,attr,omitempty"`
}

type ContentProtection struct {
	SchemeIdUri        string             `xml:"schemeIdUri,attr"`
	Value              string             `xml:"value,attr,omitempty"`
	ClearKeyLicenseURL *ClearKeyLicenseURL `xml:"clearkey:Laurl,omitempty"`
}

type ClearKeyLicenseURL struct {
	LicenseType string `xml:"Lic_type,attr,omitempty"`
	URL         string `xml:",chardata"`
}

var _ interfaces.StreamHandler = (*MPDHandler)(nil)
