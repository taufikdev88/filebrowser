package share

import (
	"github.com/gtsteffaniak/filebrowser/backend/database/users"
)

// FrontendShareInfo is share presentation and behavior for visitors (stored in share_settings JSON).
type FrontendShareInfo struct {
	ShareTheme           string              `json:"shareTheme,omitempty"`
	DisableAnonymous     bool                `json:"disableAnonymous,omitempty"`
	DisableThumbnails    bool                `json:"disableThumbnails,omitempty"`
	KeepAfterExpiration  bool                `json:"keepAfterExpiration,omitempty"`
	ThemeColor           string              `json:"themeColor,omitempty"`
	Banner               string              `json:"banner,omitempty"`
	Title                string              `json:"title,omitempty"`
	Description          string              `json:"description,omitempty"`
	Favicon              string              `json:"favicon,omitempty"`
	QuickDownload        bool                `json:"quickDownload,omitempty"`
	HideNavButtons       bool                `json:"hideNavButtons,omitempty"`
	DisableSidebar       bool                `json:"disableSidebar"`
	DownloadURL          string              `json:"downloadURL,omitempty"`
	ShareURL             string              `json:"shareURL,omitempty"`
	FaviconUrl           string              `json:"faviconUrl,omitempty"`
	BannerUrl            string              `json:"bannerUrl,omitempty"`
	DisableShareCard     bool                `json:"disableShareCard,omitempty"`
	EnforceDarkLightMode string              `json:"enforceDarkLightMode,omitempty"` // "dark" or "light"
	ViewMode             string              `json:"viewMode,omitempty"`             // default view mode for anonymous users
	EnableOnlyOffice     bool                `json:"enableOnlyOffice,omitempty"`
	ShareType            string              `json:"shareType"`
	AllowDelete          bool                `json:"allowDelete,omitempty"`
	AllowCreate          bool                `json:"allowCreate,omitempty"`
	AllowModify          bool                `json:"allowModify,omitempty"`
	DisableFileViewer    bool                `json:"disableFileViewer,omitempty"`
	DisableDownload      bool                `json:"disableDownload,omitempty"`
	AllowReplacements    bool                `json:"allowReplacements,omitempty"`
	SidebarLinks         []users.SidebarLink `json:"sidebarLinks"`
	HasPassword          bool                `json:"hasPassword,omitempty"`
	ShowHidden           bool                `json:"showHidden,omitempty"`
	DisableLoginOption   bool                `json:"disableLoginOption"`
	SourceURL            string              `json:"sourceURL,omitempty"`
	CanEditShare         bool                `json:"canEditShare,omitempty"`
}

// CreateShare is the POST /api/share JSON body: presentation options plus routing, password, and optional owner username (resolved to userID server-side).
type CreateShare struct {
	FrontendShareInfo
	Username                 string   `json:"username,omitempty"`
	Hash                     string   `json:"hash,omitempty" storm:"id,index"`
	Source                   string   `json:"source,omitempty"` // configured source name
	Path                     string   `json:"path,omitempty"`
	Password                 string   `json:"password,omitempty"`
	Expires                  string   `json:"expires,omitempty"`
	Unit                     string   `json:"unit,omitempty"`
	MaxBandwidth             int      `json:"maxBandwidth,omitempty"` // KB/s cap; 0 = unlimited
	AllowedUsernames         []string `json:"allowedUsernames,omitempty"`
	PerUserDownloadLimit     bool     `json:"perUserDownloadLimit,omitempty"`
	ExtractEmbeddedSubtitles bool     `json:"extractEmbeddedSubtitles,omitempty"`
	DownloadsLimit           int      `json:"downloadsLimit,omitempty"`
}

// Share is the persisted share: embedded CreateShare (routing + presentation) plus server-only columns.
type Share struct {
	CreateShare
	Expire        int64          `json:"expire"`
	PasswordHash  string         `json:"-"`
	UserID        uint64         `json:"userID"`
	Token         string         `json:"token,omitempty"`
	Downloads     int            `json:"downloads"`
	UserDownloads map[string]int `json:"userDownloads,omitempty"`
	Version       int            `json:"version,omitempty"`
	OwnerUsername string         `json:"username,omitempty"`
	DisplaySource string         `json:"source,omitempty"`
	PathExists    bool           `json:"pathExists,omitempty"`
}

// LegacyShare embeds Share for Bolt/Storm paths that persist shares as JSON with password_hash
type LegacyShare struct {
	Share
	PasswordHash string `json:"password_hash,omitempty"`
}

// ToShare returns a Share for SQLite/runtime: bcrypt hash on PasswordHash, CreateShare.Password cleared.
func (l *LegacyShare) ToShare() Share {
	s := l.Share
	if l.PasswordHash != "" {
		s.Password = l.PasswordHash
	}
	return s
}
