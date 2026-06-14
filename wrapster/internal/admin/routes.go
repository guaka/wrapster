package admin

const (
	WrapsterNASName = "Wrapster NAS"
	WrapsterHubName = "Wrapster Hub"

	AdminRoute       = "/admin"
	AdminRouteSlash  = AdminRoute + "/"
	AdminAPIRoot     = AdminRoute + "/api"
	AdminAPIPrefix   = AdminAPIRoot + "/"
	AdminAPIOverview = AdminAPIRoot + "/overview"
	AdminAPIStatus   = AdminAPIRoot + "/status"
	AdminAPIIdentity = AdminAPIRoot + "/identity"
	AdminAPIConfig   = AdminAPIRoot + "/config"
	AdminAPIAuthCache = AdminAPIRoot + "/auth-cache"
	AdminAPIPolicy   = AdminAPIRoot + "/policy"
	AdminAPIFipsNsec = AdminAPIRoot + "/fips-nsec"
	AdminAPIFipsPeerCheck = AdminAPIRoot + "/fips-peer-check"

	SetupRoute      = "/setup"
	SetupRouteSlash = SetupRoute + "/"
	SetupAPIRoot    = SetupRoute + "/api"
	SetupAPIAlias   = SetupAPIRoot + "/"
	SetupAPIStatus  = SetupAPIRoot + "/status"
	SetupAPIConfig  = SetupAPIRoot + "/config"
	SetupAPIFipsNsec = SetupAPIRoot + "/fips-nsec"
	SetupAPIFipsPeerCheck = SetupAPIRoot + "/fips-peer-check"
	SetupAPITestJellyfin  = SetupAPIRoot + "/test/jellyfin"
	SetupAPITestJellyfinRandomSong = SetupAPIRoot + "/test/jellyfin-random-song"
	SetupAPITestJellyfinRandomSongStream = SetupAPITestJellyfinRandomSong + "/stream/"
	SetupAPITestPlex = SetupAPIRoot + "/test/plex"
	SetupAPIFaviconSVG = SetupRoute + "/favicon.svg"
	SetupAPIFaviconICO = SetupRoute + "/favicon.ico"
)
