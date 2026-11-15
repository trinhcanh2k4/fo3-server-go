// Package api wires the Gin router: Phase 1 spine handlers, statics,
// CDN files, telemetry sinks and the fallback proxy to the Python
// server for domains that are not ported yet (the "cua lui").
package api

import (
	"compress/gzip"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"fo3server/internal/config"
	"fo3server/internal/game"
	"fo3server/internal/store"
)

// Deps bundles the API's dependencies.
type Deps struct {
	AdminDir string // built React admin (dist), served under /admin/
	Store        *store.Store
	Cfg          *config.Config
	Roster       *game.Roster
	ResponsesDir string
	AssetsDir    string
	GamedataDir  string
	PublicIP     string // production IP for /game/my/ipaddress
	PublicHost   string // production domain for the messenger service
}

// Server is the API router.
type Server struct {
	d     Deps
	proxy *httputil.ReverseProxy
	rt    *roomTimers
	gr    *roomRegistry
	mo    *matchOwners
}

// New builds the Gin engine with all Phase 1 routes.
func New(d Deps) *gin.Engine {
	s := &Server{d: d}
	s.startForfeitSweeper()
	if d.Cfg.PythonBase != "" {
		if u, err := url.Parse(d.Cfg.PythonBase); err == nil {
			s.proxy = httputil.NewSingleHostReverseProxy(u)
		}
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), s.gunzip(), s.resolveUser(), s.logRequests())

	// Spine (boot chain).
	r.GET("/info/server/time", s.serverTime)
	r.GET("/info/server/locale", s.serverLocale)
	r.GET("/info/server/config", s.serverConfig)
	r.POST("/publisher/garena/sign", s.garenaSign)
	r.Any("/game/my/ipaddress", s.myIP)
	r.GET("/game/my/tactic/all", s.tacticAll)
	r.Any("/game/my/tactic/:i", s.tacticOne)
	r.POST("/web/login", s.webAuth)
	r.POST("/launcher/login", s.launcherLogin)
	r.Any("/coach/me/squad", s.coachSquad)
	r.Any("/game/my/team/captain", s.teamCaptain)
	r.Any("/game/my/team/kickers/:idx", s.teamKickers)
	r.GET("/club/fee/creation", s.clubFeeCreation)
	r.Any("/club/fee/expansion/:n", s.clubFeeExpansion)
	r.POST("/club/create", s.clubCreate)
	r.GET("/club/me", s.clubMe)
	r.DELETE("/club/me", s.clubMeDelete)
	r.POST("/club/me", s.clubMePost)
	r.POST("/club/me/delete/cancel", s.clubMeUndelete)
	r.GET("/club/find", s.clubFind)
	r.GET("/club/me/members", s.clubMembers)
	r.PUT("/club/me/member/leave", s.clubLeave)
	r.POST("/club/me/member/leave", s.clubLeave)
	r.POST("/club/me/member/:mid", s.clubMemberGrade)
	r.PUT("/club/me/member/exclude/:mid", s.clubExclude)
	r.POST("/club/me/member/exclude/:mid", s.clubExclude)
	r.GET("/club/me/join/requests", s.clubJoinRequests)
	r.PUT("/club/me/join/:rid", s.clubJoinDecide)
	r.DELETE("/club/me/join/:rid", s.clubJoinDecide)
	r.POST("/club/me/join/:rid", s.clubJoinDecide)
	r.POST("/club/me/expand/:n", s.clubExpand)
	r.POST("/club/me/settings/:what", s.clubSettings)
	r.POST("/club/me/settings/:what/:value", s.clubSettings)
	r.PUT("/club/me/settings/:what", s.clubSettings)
	r.GET("/club/me/posts", s.clubPosts)
	r.POST("/club/me/post", s.clubPost)
	r.POST("/club/me/post/:pid", s.clubPost)
	r.DELETE("/club/me/post/:pid", s.clubPost)
	r.POST("/club/me/message", s.clubMessage)
	r.POST("/players/recontract/preview", s.recontractPreview("player"))
	r.PUT("/players/recontract/preview", s.recontractPreview("player"))
	r.POST("/players/recontract", s.recontractApply("player"))
	r.POST("/staff/recontract/preview", s.recontractPreview("staff"))
	r.PUT("/staff/recontract/preview", s.recontractPreview("staff"))
	r.POST("/staff/recontract", s.recontractApply("staff"))
	r.Any("/staff/recruit", s.staffRecruitList)
	r.Any("/staff/recruit/:recruitid", s.staffRecruit)
	r.Any("/staff/appoint/:staffid", s.staffAppoint)
	r.Any("/staff/unappoint/:staffid", s.staffUnappoint)
	r.Any("/staff/enhance/:staffid", s.staffEnhance)
	r.Any("/staff/study/:lid", s.staffStudyStart)
	r.Any("/staff/study/:lid/complete", s.staffStudyComplete)
	r.Any("/transfer/top/50", s.transferTop)
	r.Any("/transfer/top/50/position/:position", s.transferTop)
	r.Any("/transfer/search/:spid", s.transferSearch)
	r.Any("/transfer/purchase/:spid/:grade/:price/purchase", s.transferPurchase)
	r.Any("/transfer/price/:spid/:grade", s.transferPrice)
	r.POST("/transfer/offer", s.transferOffer)
	r.GET("/transfer/my/boostfactor", s.transferBoostFactor)
	r.GET("/transfer/my/offer/list", s.offerList)
	r.Any("/transfer/my/offer/completed/:offerid", s.offerCollect)
	r.Any("/transfer/my/offer/completed/:offerid/:coupon", s.offerCollect)
	r.Any("/transfer/my/offer/:status/:offerid", s.offerCancel)
	r.POST("/transfer/offer/from/:offerid", s.offerRecreate)
	r.POST("/player/:pid/lock", s.playerLock)
	r.DELETE("/player/:pid/lock", s.playerUnlock)
	r.Any("/player/:pid/move/:where", s.playerMove)
	r.Any("/player/:pid/enhance/:bid/preview", s.enhancePreview)
	r.Any("/player/:pid/enhance/:bid", s.enhanceDo)
	r.Any("/player/:pid/enhance/:bid/:boost/:guard", s.enhanceDo)
	r.Any("/player/:pid/enhance2.5/:bid/preview", s.enhancePreview)
	r.Any("/player/:pid/enhance2.5/:bid/preview/:a", s.enhancePreview)
	r.Any("/player/:pid/enhance2.5/:bid/preview/:a/:b", s.enhancePreview)
	r.Any("/player/:pid/enhance2.5/:bid/preview/:a/:b/:cc", s.enhancePreview)
	r.Any("/player/:pid/enhance2.5/:bid", s.enhanceDo)
	r.Any("/player/:pid/enhance2.5/:bid/:boost/:guard", s.enhanceDo)
	r.Any("/player/:pid/enhance2.5/:bid/:boost/:guard/:extra", s.enhanceDo)
	r.POST("/players/trade/proposal", s.tradeProposal)
	r.POST("/players/trade/contract", s.tradeContract)
	r.POST("/trade3/proposal", s.trade3Proposal)
	r.POST("/trade3/contract", s.tradeContract)
	r.POST("/trade3/info", s.trade3Info)
	r.Any("/trade3/staticvalues", s.trade3Static)
	r.GET("/mixin/recipes", s.mixinRecipes)
	r.POST("/mixin/:rid", s.mixinDo)
	r.GET("/shop/list/STSHOP", s.stShopList)
	r.POST("/room", s.roomCreate("ladder_pvp_1v1"))
	// Club friendly (đá clan): waiting-room browse + merge
	// negotiation + start. Room ids here are host coach ids.
	// Registered before the /room/:rid wildcards; the static
	// "club" segment coexists with :rid like /stadium/list
	// does with /stadium/:id.
	r.POST("/room/club", s.clubRoomList)
	r.POST("/room/club/count", s.clubRoomCount)
	r.POST("/room/club/:a/info", s.clubRoomInfo)
	r.POST("/room/club/:a/request/:b", s.clubRoomRequest)
	r.POST("/room/club/:a/accept/:b", s.clubRoomAccept)
	r.POST("/room/club/:a/reject/:b", s.clubRoomReject)
	r.POST("/room/club/:a/cancel/:b", s.clubRoomCancel)
	r.POST("/room/club/:a", s.clubRoomStart)
	r.POST("/room/club/:a/", s.clubRoomStart)
	r.POST("/exhibition/room", s.roomCreate("pvp_exhibition"))
	r.GET("/room/:rid", s.roomGet)
	r.Any("/room/:rid/leave", s.roomLeave)
	r.Any("/room/:rid/matchmaking", s.roomMatchmaking)
	r.Any("/room/:rid/side/:side/ready", s.roomSideReady)
	r.Any("/room/:rid/invite/rejection/from/:fid", s.roomInviteReject)
	r.Any("/room/:rid/invite/:cid", s.roomInvite)
	r.Any("/exhibition/room/:rid/team", s.exhibitionTeamChange)
	r.Any("/room/:rid/:action", s.roomUpdate)
	r.Any("/room/:rid/:action/*rest", s.roomUpdate)
	// Admin API (Phase 3): separate admin auth, audit-logged.
	r.POST("/admin/api/login", s.adminLogin)
	adm := r.Group("/admin/api", s.adminAuth)
	adm.GET("/users", s.adminUsers)
	adm.POST("/users", s.adminUserCreate)
	adm.POST("/users/:uid/:action", s.adminUserAction)
	adm.POST("/coaches/:uid/:slot/set/:field", s.adminCoachSet)
	adm.POST("/coaches/:uid/:slot/reset", s.adminCoachReset)
	adm.GET("/players/:uid/:slot", s.adminPlayers)
	adm.POST("/players/:uid/:slot/:op", s.adminPlayerOp)
	adm.GET("/inventory/:uid/:slot", s.adminInventory)
	adm.POST("/inventory/:uid/:slot/:op", s.adminInventory)
	adm.GET("/shop/:kind", s.adminShop)
	adm.PUT("/shop/:kind/price", s.adminShopPrice)
	adm.POST("/shop/hide", s.adminShopHide)
	adm.GET("/settings", s.adminSettings)
	adm.PUT("/settings/:name", s.adminSettings)
	adm.GET("/ladder", s.adminLadder)
	adm.POST("/ladder/reset-season", s.adminLadderReset)
	adm.GET("/league/:uid/:slot", s.adminLeague)
	adm.POST("/league/:uid/:slot/reset", s.adminLeague)
	adm.GET("/cups/:uid/:slot", s.adminCups)
	adm.POST("/cups/:uid/:slot/leave", s.adminCups)
	adm.GET("/clubs", s.adminClubs)
	adm.POST("/clubs/:clubid/dissolve", s.adminClubDissolve)
	adm.GET("/friends/:uid/:slot", s.adminFriends)
	adm.GET("/online", s.adminOnline)
	adm.POST("/notice", s.adminNotice)
	adm.GET("/ops", s.adminOps)
	adm.GET("/telemetry", s.adminTelemetry)
	adm.GET("/squad/:uid/:slot", s.adminSquad)
	adm.GET("/config/daily", s.adminDailyGet)
	adm.PUT("/config/daily", s.adminDailyPut)
	adm.GET("/config/enhance", s.adminEnhanceGet)
	adm.PUT("/config/enhance", s.adminEnhancePut)
	adm.GET("/config/craft", s.adminCraftGet)
	adm.PUT("/config/craft", s.adminCraftPut)
	adm.GET("/config/quests", s.adminQuestsGet)
	adm.PUT("/config/quests", s.adminQuestsPut)
	adm.GET("/config/achievements", s.adminAchievementsGet)
	adm.PUT("/config/achievements", s.adminAchievementsPut)
	adm.POST("/ops/restore", s.adminOpsRestore)
	adm.POST("/inventory/:uid/:slot/bag", s.adminBagGrant)
	adm.GET("/search/players", s.adminSearchPlayers)
	adm.GET("/search/staff", s.adminSearchStaff)
	adm.GET("/search/teams", s.adminSearchTeams)
	adm.POST("/users/:uid/coaches", s.adminCoachAdd)
	adm.GET("/audit", s.adminAudit)
	adm.GET("/staff/:uid/:slot", s.adminStaff)
	adm.POST("/staff/:uid/:slot/:op", s.adminStaff)
	adm.GET("/ops/backups", s.adminBackups)
	adm.POST("/ops/backup", s.adminBackups)
	adm.POST("/ops/restart", s.adminRestart)
	r.GET("/cup/list", s.cupList)
	r.GET("/cup/record", s.cupRecord)
	r.POST("/cup/new", s.cupNew)
	r.GET("/worldcup", s.worldcup)
	r.GET("/worldcup/info/authentic/team/:teamid", s.worldcupAuthenticTeam)
	r.GET("/cup/:cupid", s.cupGet)
	r.Any("/cup/:cupid/resume", s.cupResume)
	r.Any("/cup/:cupid/test", s.cupResume)
	r.Any("/cup/:cupid/next", s.cupNext)
	r.Any("/cup/:cupid/leave", s.cupLeave)
	r.Any("/cup/:cupid/match", s.cupMatch)
	r.GET("/league2/me", s.leagueMe)
	r.GET("/league2/reward", s.leagueReward)
	r.POST("/league2/calendar", s.leagueCalendar)
	r.POST("/league2/match", s.leagueMatch(false))
	r.POST("/league2/match/sim", s.leagueMatch(true))
	r.POST("/league2/sleep", s.leagueSleep)
	// The real client POSTs these two (every other league2 call
	// is a POST too; fx routed by path only, so the "PUT" in its
	// docstring never mattered there). Registered PUT-only, the
	// client's POST fell into the unknown-path stub {result:true}
	// and the handler never ran — the owner could not abandon
	// his PvE season (journal 2026-11-14 18:49, 15-byte replies).
	r.Any("/league2/abandon", s.leagueAbandon)
	r.Any("/league2/end", s.leagueEnd)
	r.POST("/league2/sponsor/:difficulty", s.leagueSponsor)
	r.Any("/league2/sponsor/achievement", s.leagueSponsorAchievement)
	r.GET("/league2/history/players/:kind", s.leagueHistoryPlayers)
	r.Any("/league2/leader/:kind", s.leagueLeader)
	r.Any("/league2/back", s.leagueBack)
	r.POST("/match/campaign", s.matchCampaign)
	r.GET("/match/:mid", s.matchGet)
	r.Any("/match/:mid/side/:side/squad", s.matchSquad)
	r.Any("/match/:mid/gamesettings", s.matchGamesettings)
	r.Any("/match/:mid/player/ready", s.matchPlayerReady)
	r.Any("/match/:mid/side/:side/ready", s.matchSideReady)
	r.Any("/match/:mid/end", s.matchEnd)
	r.POST("/internal/relay/forfeit", s.relayForfeit)
	r.POST("/internal/relay/joinauth", s.relayJoinAuth)
	r.POST("/internal/relay/stage", s.relaySetStage)
	r.Any("/match/:mid/messages", s.matchSendMessage)
	r.Any("/match/:mid/messages/latest", s.matchMessagesLatest)
	r.GET("/info/server/2ndpass", s.secondPassInfo)
	r.Any("/2ndpass/check", s.secondPassCheck)
	r.Any("/3rdpass/check", s.thirdPassCheck)
	r.POST("/3rdpass", s.thirdPassSet)
	r.PUT("/3rdpass", s.thirdPassPut)
	r.Any("/2ndpass/verify", s.secondPassCheck)
	r.Any("/2ndpass", s.secondPassSet)
	r.Any("/2ndpass/reset", s.secondPassReset)
	r.GET("/bag", s.bagList)
	r.POST("/bag/:id/unpack", s.bagUnpack)
	r.POST("/bag/unpackall", s.bagUnpackAll)
	r.GET("/rank/teamvalue/friend", s.rankTeamvalueFriend)
	r.GET("/rank/teamvalue/club", s.rankTeamvalueClub)
	r.GET("/coach/me/teamvalue/history", s.teamvalueHistory)
	r.Any("/coach/me/hiderank", s.coachHideRank)
	r.GET("/achievement/:cid", s.achievementByCoach)
	r.GET("/ladder/:cid/info", s.ladderByCoach)
	r.GET("/coach/:cid/summary", s.coachSummary)
	r.GET("/coach/:cid", s.coachByID)
	r.GET("/contact/list", s.contactList)
	r.GET("/presence", s.presence)
	r.GET("/presence/me", s.presenceMe)
	r.Any("/contact/requesting", s.contactRequesting)
	r.Any("/contact/requested/by/:cid/reply", s.contactReply)
	r.Any("/contact/requesting/to/:cid", s.contactCancelRequesting)
	r.Any("/contact/list/:cid", s.contactFriendAction)
	r.Any("/contact/list/:cid/group", s.contactFriendAction)
	r.Any("/contact/newfriends", s.contactNewFriends)
	r.Any("/contact/group", s.contactGroup)
	r.Any("/contact/group/:gname", s.contactGroupDelete)
	r.GET("/contact/club", s.contactClub)
	r.GET("/facilities", s.facilities)
	r.GET("/club/record/:what", s.clubRecord)
	r.GET("/club/:id", s.clubGet)
	r.POST("/club/:id/join", s.clubJoin)
	r.DELETE("/club/:id/join", s.clubJoin)
	r.GET("/club/:id/facilities", s.clubFacilities)
	r.GET("/club/:id/emblem/:file", s.clubEmblem)
	r.GET("/ladder/my/info", s.ladderMyInfo)
	r.GET("/ladder/division/info", s.ladderDivisionInfo)
	r.GET("/club/ranked/division/info", s.clubRankedDivisionInfo)
	r.GET("/ladder/reward/info", s.ladderRewardInfo)
	r.Any("/ladder/reward", s.ladderReward)
	r.GET("/ladder/leaderboard", s.ladderLeaderboard)
	r.GET("/ladder/season/info", s.ladderSeasonInfo(false))
	r.GET("/club/ranked/season/info", s.ladderSeasonInfo(true))
	r.GET("/ladder/friends/info", s.ladderFriendsInfo)
	r.Any("/launcher/status", s.launcherStatus)
	r.POST("/web/register", s.webAuth)
	r.POST("/api/register", s.webAuth)
	r.GET("/account", s.account)
	r.Any("/account/option", s.accountOption)
	r.GET("/account/beginnersguide", s.beginnersGuide)
	r.GET("/coaches", s.coaches)
	r.GET("/coach/me", s.coachMe)
	r.POST("/coach/me/onready", s.coachOnReady)
	r.PUT("/coach/me/onready", s.coachOnReady)
	r.PUT("/coach/:slot/new/step/3", s.coachNewTeam)
	r.POST("/coach/:slot/new/step/3", s.coachNewTeam)
	r.PUT("/coach/:slot", s.coachCreate)
	r.POST("/coach/:slot", s.coachCreate)
	r.POST("/coach/new/reserve", s.coachReserve)
	r.PUT("/coach/new/reserve", s.coachReserve)
	r.PUT("/coach/:slot/postponed", s.coachPostponed)
	r.DELETE("/coach/:slot/postponed", s.coachPostponed)
	r.PUT("/coach/:slot/default", s.coachDefault)
	r.POST("/coach/:slot/default", s.coachDefault)
	r.POST("/sign/wait", s.signWait)
	r.PUT("/sign/wait", s.signWait)
	r.GET("/info/my/services/messenger/2", s.messengerService)
	r.GET("/specifictarget", s.specificTarget)
	r.GET("/monthlypack", s.monthlyPack)
	r.GET("/inbox/my/messages", s.inboxMessages)
	r.GET("/inbox/my/notifications", s.inboxNotifications)
	r.GET("/chat/blacklist", s.chatBlacklist)
	// Audit groups A/B (2026-11-13): world chat family, coach
	// profile, badge use, locker room, exhibition + constant stubs.
	r.Any("/chat/channel", s.chatChannel)
	r.Any("/chat/channel/:id", s.chatChannelJoin)
	r.POST("/chat/my/channel/message", s.chatSendMessage)
	r.GET("/chat/my/channel/messages/latest", s.chatChannelLatest)
	r.GET("/chat/my/messages/latest", s.chatWhisperLatest)
	r.Any("/chat/coach/:cid/channel/detail", s.chatCoachDetail)
	r.Any("/coach/me/profile", s.coachMeProfile)
	r.POST("/badge/:id/use", s.badgeUse)
	r.Any("/lockerroom/:pid/itemset", s.lockerItemset)
	r.Any("/lockerroom/:pid/jersey", s.lockerJersey)
	r.Any("/exhibition/info/points", func(c *gin.Context) {
		writeJSON(c, map[string]any{"result": true, "maxpoint": 20, "point": 0})
	})
	r.GET("/exhibition/info/authentic/team/:teamid", s.authenticTeam)
	r.GET("/shop/products/2001", func(c *gin.Context) {
		writeJSON(c, []any{s.d.Roster.ShopEntry(2001, "USE", 0)})
	})
	r.GET("/shop/remained/limited", func(c *gin.Context) { writeJSON(c, []any{}) })
	r.GET("/shop/remained/limited/STSHOP", func(c *gin.Context) { writeJSON(c, []any{}) })
	r.Any("/trade3/notification", func(c *gin.Context) {
		writeJSON(c, map[string]any{"result": true})
	})
	r.POST("/players/trade/proposal/resend", func(c *gin.Context) {
		writeJSON(c, map[string]any{"result": false, "reason": "NOT_SUPPORTED_REQUEST"})
	})
	r.Any("/publisher/nexon/2ndauth", func(c *gin.Context) {
		writeJSON(c, map[string]any{"result": "ok", "passport": "LOCAL-PASSPORT"})
	})
	r.Any("/shop/cash/charge/url", func(c *gin.Context) {
		writeJSON(c, map[string]any{"url": "about:blank"})
	})
	r.Any("/shop/cash/history/url", func(c *gin.Context) {
		writeJSON(c, map[string]any{"url": "about:blank"})
	})
	r.GET("/register", s.registerPage)
	r.GET("/web/register", s.registerPage)
	r.GET("/signup", s.registerPage)
	r.GET("/signin", s.signinPage)
	r.GET("/login", s.signinPage)
	r.GET("/", s.landingPage)
	r.GET("/web/api/status", s.webStatus)
	r.GET("/web/api/rankings", s.webRankings)
	r.GET("/shop/remained/cash", func(c *gin.Context) { writeJSON(c, []any{}) })
	r.GET("/shop/list", s.shopList)
	r.GET("/shop/list/GPSHOP", s.gpShopList)
	r.POST("/shop/purchase", s.shopPurchase)
	r.POST("/item/:id/use", s.itemUse)
	r.POST("/static/cache/item", s.staticCacheItem)
	r.GET("/achievement/me", s.achievementMe)
	r.POST("/achievement/questrewards", s.achievementQuestRewards)
	r.POST("/achievement/:group/reward/:grade", s.achievementClaim)
	r.POST("/telemetry/*any", s.telemetry)
	r.GET("/static/*any", s.staticFile)
	r.GET("/stadium/list", func(c *gin.Context) {
		writeJSON(c, s.d.Roster.StadiumList())
	})
	r.GET("/stadium/:id", func(c *gin.Context) {
		// id 1 keeps the long-standing hardcoded Mỹ Đình doc (hc's
		// exact ENDPOINTS entry); the catalog's stadium 1 is Old
		// Trafford, which the dynamic route would wrongly serve.
		if c.Param("id") == "1" {
			writeJSON(c, map[string]any{"id": 1, "name": "Sân vận động Quốc gia Mỹ Đình",
				"label":     "Sân vận động Quốc gia Mỹ Đình",
				"yearbuilt": 2008, "capacity": 68000,
				"todWeatherAvailables": map[string]any{
					"1": []any{0, 1, 2},
					"3": []any{0, 1, 2},
				}})
			return
		}
		writeJSON(c, s.d.Roster.StadiumDoc(mustAtoiParam(c.Param("id"))))
	})
	r.GET("/cdn/*any", s.cdnFile)
	r.NoRoute(s.fallback)
	return r
}

// --- middleware -------------------------------------------------------

func clientIP(c *gin.Context) string {
	ip := c.RemoteIP()
	if ip == "127.0.0.1" || ip == "::1" {
		if fwd := c.GetHeader("X-Real-IP"); fwd != "" {
			return strings.TrimSpace(strings.Split(fwd, ",")[0])
		}
		if fwd := c.GetHeader("X-Forwarded-For"); fwd != "" {
			return strings.TrimSpace(strings.Split(fwd, ",")[0])
		}
	}
	return ip
}

// localDirect reports whether the request arrived directly from
// this machine without passing through the reverse proxy: the peer
// is loopback and no proxy forwarding headers are present. Only
// such requests may use the legacy fx token form (the internal
// match-settlement calls); anything via Nginx carries X-Real-IP
// and must present a server-issued token.
func localDirect(c *gin.Context) bool {
	ip := c.RemoteIP()
	if ip != "127.0.0.1" && ip != "::1" {
		return false
	}
	return c.GetHeader("X-Real-IP") == "" && c.GetHeader("X-Forwarded-For") == ""
}

func (s *Server) gunzip() gin.HandlerFunc {
	return func(c *gin.Context) {
		// The client gzips larger bodies (telemetry, /coach/me/squad);
		// Python sniffs the magic bytes -- do the same.
		if c.Request.Body != nil && c.Request.ContentLength != 0 {
			raw, err := io.ReadAll(c.Request.Body)
			if err == nil {
				if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
					if zr, zerr := gzip.NewReader(strings.NewReader(string(raw))); zerr == nil {
						if plain, perr := io.ReadAll(zr); perr == nil {
							raw = plain
						}
					}
				}
				c.Request.Body = io.NopCloser(strings.NewReader(string(raw)))
				c.Request.ContentLength = int64(len(raw))
			}
		}
		c.Next()
	}
}

func (s *Server) resolveUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		// Tag the request so the EP ledger can attribute state saves
		// (and the EP changes in them) to the endpoint that made them.
		c.Request = c.Request.WithContext(game.WithEPSource(ctx,
			c.Request.Method+" "+c.Request.URL.Path))
		token := strings.TrimSpace(c.GetHeader("X-FO3-Token"))
		if token == "" {
			token = strings.TrimSpace(c.Query("token"))
		}
		if token == "" {
			token = strings.TrimSpace(c.Query("key"))
		}
		ip := clientIP(c)
		u, via := game.ResolveUser(ctx, s.d.Store, token, ip, localDirect(c))
		if u != nil {
			if via == "ip" || via == "lastactive" {
				s.observeAuthFallback(c, u, via, ip)
			}
			c.Set("user", u)
			c.Set("clientIP", ip)
			if token != "" {
				game.NoteActive(ctx, s.d.Store, u)
			}
		}
		c.Next()
	}
}

func (s *Server) logRequests() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Printf("%s %s -> %d (%s)", c.Request.Method, c.Request.URL.Path,
			c.Writer.Status(), time.Since(start).Round(time.Millisecond))
	}
}

// --- helpers ----------------------------------------------------------

func mustAtoiParam(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func writeJSON(c *gin.Context, obj any) {
	b, err := json.Marshal(obj)
	if err != nil {
		c.Data(http.StatusOK, "application/json", []byte(`{"result":false}`))
		return
	}
	c.Data(http.StatusOK, "application/json", b)
}

func userOf(c *gin.Context) *game.User {
	if v, ok := c.Get("user"); ok {
		return v.(*game.User)
	}
	return nil
}

func (s *Server) loadState(c *gin.Context) (game.State, *game.User, bool) {
	u := userOf(c)
	if u == nil {
		writeJSON(c, map[string]any{"result": false})
		return nil, nil, false
	}
	st, err := game.LoadState(c.Request.Context(), s.d.Store, u.ID)
	if err != nil {
		writeJSON(c, map[string]any{"result": false})
		return nil, nil, false
	}
	return st, u, true
}

func (s *Server) fallback(c *gin.Context) {
	if s.serveAdmin(c) {
		return
	}
	if s.proxy != nil {
		s.proxy.ServeHTTP(c.Writer, c.Request)
		return
	}
	// Python-compatible fallbacks: unknown GET {}, unknown POST result true.
	if c.Request.Method == http.MethodPost {
		writeJSON(c, map[string]any{"result": true})
		return
	}
	// Statics by the from_file rule also cover non-/static paths
	// (e.g. /account/features -> responses/account_features.json).
	if hit := s.fromFile(c.Request.URL.Path); hit != nil {
		c.Data(http.StatusOK, "application/json", hit)
		return
	}
	writeJSON(c, map[string]any{})
}

// serveAdmin serves the built React admin. When an admin
// subdomain is configured (FO3_ADMIN_HOST), the panel lives at
// the ROOT of that subdomain (behind Nginx basic auth) and the
// game domain answers /admin* with 404. /admin/api/* is routed
// separately on both hosts.
func (s *Server) serveAdmin(c *gin.Context) bool {
	p := c.Request.URL.Path
	if s.d.AdminDir == "" || strings.HasPrefix(p, "/admin/api/") ||
		c.Request.Method != http.MethodGet {
		return false
	}
	host := requestHost(c)
	if host == gameHost && (p == "/admin" || strings.HasPrefix(p, "/admin/")) {
		c.Status(http.StatusNotFound)
		return true
	}
	if host != adminHost {
		return false
	}
	rel := strings.TrimPrefix(p, "/")
	if rel == "" {
		rel = "index.html"
	}
	full := filepath.Join(s.d.AdminDir, rel)
	if st, err := os.Stat(full); err == nil && !st.IsDir() {
		if strings.HasSuffix(rel, ".html") {
			// The SPA shell must never be cached: a stale index
			// points at an old JS bundle and the panel then shows
			// empty/stale data (user reports 2026-11-13).
			c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		c.File(full)
		return true
	}
	index := filepath.Join(s.d.AdminDir, "index.html")
	if _, err := os.Stat(index); err == nil {
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.File(index)
		return true
	}
	return false
}

func (s *Server) fromFile(path string) []byte {
	name := strings.Trim(path, "/")
	name = strings.ReplaceAll(name, "/", "_") + ".json"
	f := filepath.Join(s.d.ResponsesDir, name)
	b, err := store.ReadFileCached(f)
	if err != nil {
		return nil
	}
	return b
}

// --- spine handlers ---------------------------------------------------

func (s *Server) serverTime(c *gin.Context) {
	writeJSON(c, map[string]any{"timestamp": time.Now().UnixMilli(), "timezone": -420})
}

// serverLocale ports hc ep_locale. BOOT-CRITICAL: "language" tells
// the client which string table to apply (it then loads
// /cdn/externalAssets/language_<lang>.big). While this was only
// served by the Python fallback, stopping Python (Phase 4) made Go
// answer {} and EVERY UI caption rendered as its raw key
// (FIFAPASSWORD_INPUT_TITLE etc.) — user report 2026-11-13.
func (s *Server) serverLocale(c *gin.Context) {
	writeJSON(c, map[string]any{"result": true, "language": "vie",
		"url": "https://" + s.d.PublicHost + "/cdn/"})
}

// serverConfig ports hc ep_server_config (boot-time redirector /
// notification endpoints + region).
func (s *Server) serverConfig(c *gin.Context) {
	writeJSON(c, map[string]any{
		"redirector":   map[string]any{"host": s.d.PublicIP, "port": 28000},
		"notification": map[string]any{"host": s.d.PublicIP, "port": 28001},
		"region":       "VN", "status": "ok"})
}

func (s *Server) myIP(c *gin.Context) {
	if c.Request.Method == http.MethodPut {
		// hc.ep_my_ip for PUT /game/my/ipaddress
		writeJSON(c, map[string]any{"result": true, "ipaddress": "127.0.0.1"})
		return
	}
	writeJSON(c, map[string]any{"result": true, "ipaddress": s.d.PublicIP})
}

// garenaSign ports fx.ep_garena_sign.
func (s *Server) garenaSign(c *gin.Context) {
	ctx := c.Request.Context()
	body, _ := io.ReadAll(c.Request.Body)
	token := ""
	if len(body) > 0 {
		var req map[string]any
		if json.Unmarshal(body, &req) == nil {
			token = strings.TrimSpace(game.Str(req["token"]))
			if token == "" {
				token = strings.TrimSpace(game.Str(req["key"]))
			}
		}
	}
	if token == "" {
		token = strings.TrimSpace(c.Query("token"))
	}
	if token == "" {
		token = strings.TrimSpace(c.Query("key"))
	}
	// Python: the sign handler re-resolves by the presented token,
	// overriding the request-level (IP) resolution.
	var u *game.User
	if token != "" {
		u, _ = game.ResolveUser(ctx, s.d.Store, token, clientIP(c), localDirect(c))
	}
	if u == nil {
		u = userOf(c)
	}
	if u == nil {
		u, _ = game.UserByUsername(ctx, s.d.Store, "admin")
	}
	if u == nil {
		writeJSON(c, map[string]any{"result": "ok", "token": "LOCAL-SIGNED-TOKEN", "uid": 1})
		return
	}
	if token == "" {
		token = "FO3-" + u.Username + "-" + itoa(u.ID)
	}
	ip := clientIP(c)
	_, _ = s.d.Store.DB.ExecContext(ctx, "UPDATE users SET last_ip = ? WHERE id = ?", ip, u.ID)
	_ = s.d.Store.Redis.Set(ctx, "ipuser:"+ip, u.Username, 24*time.Hour).Err()
	writeJSON(c, map[string]any{"result": "ok", "token": token, "uid": u.ID})
}

func itoa(v int64) string { return fmt.Sprint(v) }

func intToStr(v int64) string { return fmt.Sprint(v) }

// webAuth ports fx's web register/login (the launcher flow).
func (s *Server) webAuth(c *gin.Context) {
	if on, msg := game.MaintenanceOn(c.Request.Context(), s.d.Store); on {
		writeJSON(c, map[string]any{"result": false, "reason": "MAINTENANCE", "message": msg})
		return
	}
	ctx := c.Request.Context()
	body, _ := io.ReadAll(c.Request.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	username := strings.ToLower(strings.TrimSpace(game.Str(req["username"])))
	password := game.Str(req["password"])
	action := game.Str(req["action"])
	if action == "" {
		if strings.Contains(c.Request.URL.Path, "register") {
			action = "register"
		} else {
			action = "login"
		}
	}
	if !validUsername(username) {
		writeJSON(c, map[string]any{"result": false, "message": "Tên tài khoản phải từ 3-24 ký tự (chữ thường, số, dấu gạch dưới)."})
		return
	}
	if len(password) < 3 {
		writeJSON(c, map[string]any{"result": false, "message": "Mật khẩu phải có ít nhất 3 ký tự."})
		return
	}
	ip := clientIP(c)
	if action != "register" && s.loginBlocked(ctx, "lg:", ip, username) {
		writeJSON(c, map[string]any{"result": false, "message": "Đăng nhập bị tạm khóa do sai mật khẩu nhiều lần từ địa chỉ này. Thử lại sau 15 phút."})
		return
	}
	existing, _ := game.UserByUsername(ctx, s.d.Store, username)
	if action == "register" {
		if s.registerBlocked(ctx, ip) {
			writeJSON(c, map[string]any{"result": false, "message": "Bạn đã đăng ký quá nhiều lần. Vui lòng thử lại sau."})
			return
		}
		if ok, msg := s.registrationGate(ctx, game.Str(req["invite_code"])); !ok {
			writeJSON(c, map[string]any{"result": false, "message": msg})
			return
		}
		if existing != nil {
			writeJSON(c, map[string]any{"result": false, "message": "Tài khoản này đã tồn tại!"})
			return
		}
		u, err := game.CreateUser(ctx, s.d.Store, username, password)
		if err != nil {
			writeJSON(c, map[string]any{"result": false, "message": "Không tạo được tài khoản."})
			return
		}
		s.noteRegister(ctx, ip)
		_ = game.SaveState(ctx, s.d.Store, u.ID, game.DefaultEmptyState())
		token, _ := game.IssueToken(ctx, s.d.Store, u, ip)
		writeJSON(c, map[string]any{
			"result": true, "token": token, "uid": u.ID, "username": username,
			"message": "Đăng ký thành công tài khoản '" + username + "'! Hãy đăng nhập trên FO3Launcher.exe để vào game.",
		})
		return
	}
	if existing == nil {
		s.noteLoginFailure(ctx, "lg:", ip, username)
		writeJSON(c, map[string]any{"result": false, "message": "Tài khoản không tồn tại! Vui lòng đăng ký trước."})
		return
	}
	if !game.VerifyPassword(ctx, s.d.Store, existing, password) {
		s.noteLoginFailure(ctx, "lg:", ip, username)
		writeJSON(c, map[string]any{"result": false, "message": "Mật khẩu không chính xác!"})
		return
	}
	s.noteLoginSuccess(ctx, "lg:", ip, username)
	token, _ := game.IssueToken(ctx, s.d.Store, existing, ip)
	realIP := s.d.PublicIP
	if ip == "127.0.0.1" || ip == "::1" {
		realIP = "127.0.0.1"
	}
	writeJSON(c, map[string]any{
		"result": true, "token": token, "uid": existing.ID, "username": username,
		"message":   "Đăng nhập thành công (" + username + ")! Đang khởi động FIFA Online 3...",
		"real_ip":   realIP, "game_port": 28000, "noti_port": 28001,
	})
}

func validUsername(s string) bool {
	if len(s) < 3 || len(s) > 24 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

var completedGuides = []string{"setcontroller", "create", "league", "recontract", "swap", "use"}

var extraAccountFeatures = map[string]any{
	"messenger":    map[string]any{},
	"contact":      map[string]any{},
	"systemmessage": map[string]any{},
	"chatting":     map[string]any{},
	"laddermatch": map[string]any{
		"vs1": map[string]any{}, "vs2": map[string]any{}, "vs3": map[string]any{},
		"roleplay3vs3": map[string]any{}, "mgr1": map[string]any{},
	},
	"friendlymatch": map[string]any{
		"quickmatch": map[string]any{}, "randommatch": map[string]any{}, "custommatch": map[string]any{},
		"managermode": map[string]any{"vsrandom": map[string]any{}},
		// clubmatch gates the club hub's "create club match"
		// button (ClubInformationTabPage checks
		// /friendlymatch/clubmatch); without it the whole
		// club-vs-club feature is invisible in the client.
		"clubmatch": map[string]any{},
	},
	"livecenter": map[string]any{},
	"freeroambuttons": map[string]any{
		"quest": map[string]any{},
	},
	"manager2": map[string]any{"overlay": map[string]any{}},
	"mainmenu": map[string]any{"club": map[string]any{}},
}

var soundDefaults = map[string]any{
	"Master": 10, "Commentary": 10, "PAVolume": 10, "PlayerCalls": 10,
	"SFX": 10, "Crowd": 10, "BGM": 10,
}

// account ports hc.ep_account + fx.ep_account_ext.
func (s *Server) account(c *gin.Context) {
	raw, _ := store.ReadFileCached(filepath.Join(s.d.ResponsesDir, "account_base.json"))
	if raw == nil {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	var acc map[string]any
	if err := json.Unmarshal(raw, &acc); err != nil {
		writeJSON(c, map[string]any{"result": false})
		return
	}
	st, u, ok := s.loadState(c)
	if !ok {
		return
	}
	_ = u
	// guides: base + completed guides
	guides := game.List(acc["guides"])
	seen := map[string]bool{}
	for _, g := range guides {
		seen[game.Str(g)] = true
	}
	for _, g := range completedGuides {
		if !seen[g] {
			guides = append(guides, g)
		}
	}
	acc["guides"] = guides
	// features: merge extras (top level + friendlymatch/freeroambuttons/mainmenu subkeys)
	feats := game.Map(acc["features"])
	if feats == nil {
		feats = map[string]any{}
	}
	for k, v := range extraAccountFeatures {
		if _, ok := feats[k]; !ok {
			feats[k] = v
		}
	}
	for _, top := range []string{"friendlymatch", "freeroambuttons", "mainmenu"} {
		sub := game.Map(feats[top])
		if sub == nil {
			sub = map[string]any{}
			feats[top] = sub
		}
		for sk, sv := range game.Map(extraAccountFeatures[top]) {
			if _, ok := sub[sk]; !ok {
				sub[sk] = sv
			}
		}
	}
	// Club-match visibility gate: ClubInformationTabPage's
	// showClubFriendlyBtn checks community/myclub/clubinfo
	// for a "clubmatch" entry (the button's click handler
	// separately checks /friendlymatch/clubmatch, merged
	// above). The base doc carries clubinfo as an empty
	// node, so add the leaf in place, siblings untouched.
	if ci := game.Map(game.Map(game.Map(feats["community"])["myclub"])["clubinfo"]); ci != nil {
		if _, ok := ci["clubmatch"]; !ok {
			ci["clubmatch"] = map[string]any{}
		}
	}
	// strips (fx._strip_liveboost + _strip_hidden_ui)
	if lc := game.Map(feats["livecenter"]); lc != nil {
		delete(lc, "boost")
	}
	delete(feats, "clubranking")
	// Feature flags (admin settings "feature_flags"): show_koth /
	// show_broadcast / show_webevent default false = today's
	// hidden state (the user's choice); setting one true restores
	// that entry instead of stripping it.
	// The base features Go serves never contained these entries
	// (Python's file did, and Python stripped them at serve time),
	// so a true flag ADDS the entry; false deletes (a no-op here).
	if mm := game.Map(feats["mainmenu"]); mm != nil {
		if game.Flag("show_koth", false) {
			mm["regionalleague"] = map[string]any{}
		} else {
			delete(mm, "regionalleague")
		}
	}
	if es := game.Map(feats["esports"]); es != nil {
		if game.Flag("show_koth", false) {
			es["regionalleague"] = map[string]any{}
		} else {
			delete(es, "regionalleague")
		}
	}
	if fr := game.Map(feats["freeroambuttons"]); fr != nil {
		if game.Flag("show_broadcast", false) {
			fr["broadcast"] = map[string]any{}
		} else {
			delete(fr, "broadcast")
		}
		if game.Flag("show_webevent", false) {
			fr["webevent"] = map[string]any{}
		} else {
			delete(fr, "webevent")
		}
	}
	acc["features"] = feats
	// option overlay from the user's saved options
	if opt := game.Map(acc["option"]); opt != nil {
		if saved := game.Map(st["option"]); saved != nil {
			for k, v := range saved {
				opt[k] = v
			}
		}
		sound := game.Map(opt["soundOption"])
		merged := map[string]any{}
		for k, v := range soundDefaults {
			merged[k] = v
		}
		for k, v := range sound {
			merged[k] = v
		}
		opt["soundOption"] = merged
		opt["controlOption"] = s.d.Roster.ControlOptions(game.Map(opt["controlOption"]))
		opt["cameraOption"] = s.d.Roster.CameraOptions(game.Map(opt["cameraOption"]))
	}
	// second-password status
	hasPass := game.HasSecondPass(st)

	status := "notset"
	if hasPass {
		status = "permitted"
	}
	acc["status"] = map[string]any{"status": status}
	writeJSON(c, acc)
}

func (s *Server) beginnersGuide(c *gin.Context) {
	writeJSON(c, map[string]any{"result": true, "guides": completedGuides})
}

// coaches ports hc.ep_coaches (coach-select screen): the body is
// exactly {coachlist, defaultcoachid}; each entry is the stored coach
// dict plus frontplayer/summary/teamvalue/levelinfo/recoveryperiod.
func (s *Server) coaches(c *gin.Context) {
	st, u, ok := s.loadState(c)
	if !ok {
		return
	}
	if game.PurgeDeletedCoaches(st) {
		_ = game.SaveState(c.Request.Context(), s.d.Store, u.ID, st)
	}
	list := []any{}
	usable := map[string]bool{}
	for _, coach := range game.Coaches(st) {
		cid := game.Str(coach["_id"])
		state := game.Str(coach["state"])
		if state == "" || state == "active" {
			usable[cid] = true
		}
		slot := game.Int(coach["slot"])
		me := s.d.Roster.CoachMe(st, slot, u)
		team := game.Map(me["team"])
		teamid := game.Int(coach["teamid"])
		entry := map[string]any{}
		for k, v := range coach {
			entry[k] = v
		}
		if fp := frontPlayer(s.d.Roster, team, teamid); fp != nil {
			entry["frontplayer"] = fp
		}
		entry["summary"] = me["summary"]
		val := 0.0
		for _, pv := range game.List(team["players"]) {
			p := game.Map(pv)
			g := game.Int(p["grade"])
			if g < 1 {
				g = 1
			}
			val += s.d.Roster.CardPrice(game.Int(p["spid"]), g)
		}
		entry["teamvalue"] = map[string]any{"val": int64(val)}
		entry["levelinfo"] = me["levelinfo"]
		entry["recoveryperiod"] = 300
		if da := coach["deleteat"]; da != nil {
			entry["hideat"] = da
		}
		list = append(list, entry)
	}
	def := game.Str(st["defaultcoachid"])
	if !usable[def] {
		def = ""
	}
	writeJSON(c, map[string]any{"coachlist": list, "defaultcoachid": def})
}

// frontPlayer ports the ep_coaches preview pick: the captain among the
// starters (or the best-rated starter), with the team kit availables.
func frontPlayer(r *game.Roster, team map[string]any, teamid int) map[string]any {
	sq := game.Map(team["squad"])
	starters := map[string]bool{}
	for _, ev := range game.List(sq["positions"]) {
		e := game.Map(ev)
		if pos := game.Int(e["pos"]); pos > 0 && pos < 28 {
			starters[game.Str(e["id"])] = true
		}
	}
	var front map[string]any
	best := -1
	for _, pv := range game.List(team["players"]) {
		p := game.Map(pv)
		if !starters[game.IID(p)] {
			continue
		}
		if game.IID(p) == game.Str(team["captain"]) {
			front = p
			break
		}
		if ovr := r.PlayerOvr(p); ovr > best {
			best = ovr
			front = p
		}
	}
	if front == nil {
		return nil
	}
	fp := map[string]any{}
	for k, v := range front {
		fp[k] = v
	}
	fp["kit"] = map[string]any{
		"home":   map[string]any{"id": teamid<<10 | 0},
		"away":   map[string]any{"id": teamid<<10 | 1},
		"goalie": map[string]any{"id": teamid<<10 | 2},
	}
	if lr := game.Map(front["lr"]); lr != nil {
		fp["lr"] = lr
	} else {
		fp["lr"] = map[string]any{}
	}
	return fp
}

func (s *Server) coachMe(c *gin.Context) {
	st, u, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	me := s.d.Roster.CoachMe(st, slot, u)
	if p := c.Query("path"); p != "" {
		if v, exists := me[p]; exists {
			writeJSON(c, v)
			return
		}
	}
	writeJSON(c, me)
}

func (s *Server) coachOnReady(c *gin.Context) {
	st, u, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.DefaultSlot(st)
	myCID, _ := myCoachID(st)
	notes := s.userNotes(c.Request.Context(), st, myCID)
	resources := s.d.Roster.CoachMeResources(st, slot, u)
	resources = append([]any{s.facilitiesResource(c, myCID)}, resources...)
	resources = append(resources, map[string]any{"uri": "/inbox/my/notifications", "body": notes})
	writeJSON(c, map[string]any{"result": true, "resources": resources})
}

// coachDefault ports hc.ep_default_coach (context swap included;
// daily-login rewards arrive with the Phase 2 quest domain).
func (s *Server) coachDefault(c *gin.Context) {
	st, u, ok := s.loadState(c)
	if !ok {
		return
	}
	slot := game.Int(c.Param("slot"))
	valid := false
	for _, coach := range game.Coaches(st) {
		state := "active"
		if _, has := coach["state"]; has {
			state = game.Str(coach["state"])
		}
		if game.Int(coach["slot"]) == slot && state == "active" {
			valid = true
		}
	}
	if !valid {
		writeJSON(c, map[string]any{"result": false, "reason": "INVALID_COACH"})
		return
	}
	prev := game.DefaultSlot(st)
	if prev != slot {
		contextKeys := []string{"league2", "cups", "offers", "match", "trade", "cup_pending"}
		contexts := game.Map(st["coachcontexts"])
		if contexts == nil {
			contexts = map[string]any{}
			st["coachcontexts"] = contexts
		}
		prevCtx := map[string]any{}
		for _, k := range contextKeys {
			if v, exists := st[k]; exists {
				prevCtx[k] = v
				delete(st, k)
			}
		}
		contexts[itoa2(prev)] = prevCtx
		if next := game.Map(contexts[itoa2(slot)]); next != nil {
			for _, k := range contextKeys {
				if v, exists := next[k]; exists {
					st[k] = v
				}
			}
		}
	}
	st["defaultcoachid"] = game.CoachIDForUser(u.ID, slot)
	_ = s.d.Roster.DailyLogin(st) // first login of the day: reward to the Bag
	// fix_formation on the newly default team
	if team := game.TeamOf(st, slot); team != nil {
		if _, had := team["formations"]; had {
			delete(team, "formations")
			sq := game.Map(team["squad"])
			if sq != nil && (sq["formation"] == nil || game.Int(sq["formation"]) == 1) {
				var teamid int
				for _, coach := range game.Coaches(st) {
					if game.Int(coach["slot"]) == slot {
						teamid = game.Int(coach["teamid"])
					}
				}
				if forms := s.d.Roster.TeamFormations(teamid); len(forms) > 0 {
					sq["formation"] = game.Map(forms[0])["formationid"]
				}
			}
		}
	}
	_ = game.SaveState(c.Request.Context(), s.d.Store, u.ID, st)
	writeJSON(c, map[string]any{"result": true, "resources": s.d.Roster.CoachMeResources(st, slot, u)})
}

func itoa2(v int) string { return intToStr(int64(v)) }

func newIID() string {
	b := make([]byte, 12)
	_, _ = cryptorand.Read(b)
	const hexd = "0123456789abcdef"
	out := make([]byte, 24)
	for i, x := range b {
		out[2*i] = hexd[x>>4]
		out[2*i+1] = hexd[x&15]
	}
	return string(out)
}

// telemetry accepts every client telemetry POST and stores UI errors.
func (s *Server) telemetry(c *gin.Context) {
	body, _ := io.ReadAll(c.Request.Body)
	path := c.Request.URL.Path
	if strings.Contains(path, "ui/errors") || strings.Contains(path, "client/log") {
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		msg := ""
		cat := ""
		if payload != nil {
			msg = game.Str(payload["message"])
			if msg == "" {
				msg = game.Str(payload["type"])
			}
			cat = game.Str(payload["category"])
		}
		var uid any
		if u := userOf(c); u != nil {
			uid = u.ID
		}
		// BE_* client/log events carry a rich payload the engine
		// builds natively (BE_BLACKSCREEN flushes its internal log
		// ring; BE_CATCHUPQUEUE lists received sequence numbers).
		// Keep the full body for those: the name-only summary threw
		// away exactly the evidence needed to debug match stalls.
		if strings.HasPrefix(msg, "BE_") {
			msg = msg + "\n" + string(body)
		}
		_, _ = s.d.Store.DB.ExecContext(c.Request.Context(),
			"INSERT INTO telemetry_errors (user_id, uri, category, message) VALUES (?, ?, ?, ?)",
			uid, path, cat, trunc(msg, 30000))
	}
	writeJSON(c, map[string]any{"result": true})
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// staticFile serves responses/<path>.json (hc.from_file semantics).
// /static/info/request is a computed endpoint in Python (fx), handled
// here because Gin will not mix a static route with the /static/*any
// catch-all.
// teamColorInfo ports hc.ep_teamcolor: GET /static/info/teamcolor,
// fetched once per login; the specials half depends on the caller's
// owned cards.
func (s *Server) teamColorInfo(c *gin.Context) {
	st, _, ok := s.loadState(c)
	if !ok {
		return
	}
	team := game.TeamOf(st, game.DefaultSlot(st))
	writeJSON(c, map[string]any{
		"teamColors":        s.d.Roster.TeamColorsCatalog(),
		"teamColorSpecials": s.d.Roster.TeamColorSpecials(team),
	})
}

func (s *Server) staticFile(c *gin.Context) {
	if c.Request.URL.Path == "/static/info/request" {
		s.staticInfoRequest(c)
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/static/info/authentic/team/") {
		parts := strings.Split(strings.TrimPrefix(c.Request.URL.Path, "/static/info/authentic/team/"), "/")
		if len(parts) == 2 {
			c.Params = append(c.Params, gin.Param{Key: "teamid", Value: parts[1]})
			s.authenticTeam(c)
			return
		}
	}
	if c.Request.URL.Path == "/static/info/teamcolor" {
		s.teamColorInfo(c)
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/static/info/team/summary/") {
		// hc serves club_summary_response here (TEAM_CHANGE
		// steppers); without it the client got {} from from_file.
		idStr := strings.TrimPrefix(c.Request.URL.Path, "/static/info/team/summary/")
		if teamid, err := strconv.Atoi(idStr); err == nil {
			writeJSON(c, s.d.Roster.ClubSummaryResponse(teamid))
			return
		}
	}
	if c.Request.URL.Path == "/static/db/PE2STATICVALUES" {
		writeJSON(c, map[string]any{"bonus": map[string]any{"use": 1000},
			"simplify_step": map[string]any{"use": 0},
			"effect_price": map[string]any{"0": 5000000000, "1": 1000000000,
				"2": 100000000, "3": 10000000, "4": 1000000}})
		return
	}
	if c.Request.URL.Path == "/static/db/PLAYERTRADE" {
		pg := map[string]any{}
		for s2 := 2; s2 <= 100; s2++ {
			pg[fmt.Sprintf("%d", s2)] = game.TradeGrades(s2)
		}
		writeJSON(c, map[string]any{"playergrade": pg})
		return
	}
	if c.Request.URL.Path == "/static/db/STATICVALUES/max_coachlevel" {
		writeJSON(c, game.CoachMaxLevel)
		return
	}
	if c.Request.URL.Path == "/static/db/EXPPLAYER" {
		writeJSON(c, game.ExpPlayerTable())
		return
	}
	if c.Request.URL.Path == "/static/db/EXPCOACH" {
		writeJSON(c, game.ExpCoachTable())
		return
	}
	if c.Request.URL.Path == "/static/db/BASECONTRACTFEE" {
		writeJSON(c, game.BaseContractFeeTable())
		return
	}
	if c.Request.URL.Path == "/static/db/MATCH_RULES" {
		rules := map[string]any{"yellow_max": 2, "red_max": 1}
		cards := map[string]any{}
		for _, k := range []string{"default", "ladder", "Regional_League", "league2",
			"Cup_PvE", "WorldCup_PvE", "Exhibition", "Friendly", "Club_Match",
			"ranking_1v1", "ranking_2v2", "ranking_3v3", "custom", "quick",
			"roleplay", "Ladder_PvP_1v1", "Ladder_MGR_PvP_1v1", "Friendly_PvP_1v1"} {
			cards[k] = rules
		}
		writeJSON(c, map[string]any{"cards": cards})
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/static/db/LIVEBOOST") {
		writeJSON(c, map[string]any{"expiretime": "1970-01-01T00:00:00Z",
			"liveplayers": map[string]any{"top": []any{}}})
		return
	}
	if c.Request.URL.Path == "/static/db/SORTNATION" {
		writeJSON(c, s.d.Roster.SortNation())
		return
	}
	if c.Request.URL.Path == "/static/db/SORTLEAGUE" {
		writeJSON(c, s.d.Roster.SortLeague())
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/static/db/SORTLEAGUE/") {
		// The REAL leagues of the nation. fx answered a constant
		// [13 16 19 31 53] because its Python club_index was
		// broken upstream, and the first Go port copied the
		// constant for parity — but the room/AI steppers resolve
		// their indices against THESE endpoints, so the constant
		// pinned every nation to the same five leagues and every
		// league to the same club list (owner report 2026-11-15:
		// the bot steppers only ever showed big-league clubs,
		// and the picked index meant a different club to the
		// server than to his screen).
		nid := strings.TrimPrefix(c.Request.URL.Path, "/static/db/SORTLEAGUE/")
		ids := s.d.Roster.SortLeague()[nid]
		if ids == nil {
			ids = []int{}
		}
		writeJSON(c, ids)
		return
	}
	if c.Request.URL.Path == "/static/db/SORTTEAM" {
		writeJSON(c, s.d.Roster.SortTeam())
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/static/db/SORTTEAM/") {
		// The REAL teams of the league (see SORTLEAGUE above;
		// fx's constant here was EuroClubs).
		lid := strings.TrimPrefix(c.Request.URL.Path, "/static/db/SORTTEAM/")
		ids := s.d.Roster.SortTeam()[lid]
		if ids == nil {
			ids = []int{}
		}
		writeJSON(c, ids)
		return
	}
	if c.Request.URL.Path == "/static/db/SORTWORLDCUPTEAM" {
		writeJSON(c, s.d.Roster.SortWorldCupTeam())
		return
	}
	if c.Request.URL.Path == "/static/stadium/1" {
		writeJSON(c, map[string]any{"id": 1, "name": "Sân vận động Quốc gia Mỹ Đình",
			"yearbuilt": 2008, "capacity": 68000})
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/static/stadium/") {
		writeJSON(c, s.d.Roster.StadiumDoc(mustAtoiParam(
			strings.TrimPrefix(c.Request.URL.Path, "/static/stadium/"))))
		return
	}
	if c.Request.URL.Path == "/static/db/EXHIBITIONMATCH" {
		// fx EXHIBITION_STATIC_DB (friendly hub static).
		writeJSON(c, map[string]any{
			"recommendteams": game.EuroClubs,
			"staticvalues":   map[string]any{"Normal_count": 10, "IGR_count": 5, "VIP1_count": 5},
		})
		return
	}
	// fx registers the badges doc at four paths; the client probes
	// the lowercase/singular aliases too (audit 2026-11-13).
	if c.Request.URL.Path == "/static/db/BADGES" || c.Request.URL.Path == "/static/db/BADGE" ||
		c.Request.URL.Path == "/static/info/BADGES" || c.Request.URL.Path == "/static/info/badges" ||
		c.Request.URL.Path == "/static/info/badge" {
		writeJSON(c, game.Achieve(s.d.GamedataDir).StaticBadges())
		return
	}
	// The client reads the achievement catalogue as StaticData
	// "ACHIEVEMENT" under /static/db/ (CoachAchievementListTabPage
	// joins the payload against it: `mixed` groups + `list`). Only
	// /static/info/ACHIEVEMENT was dispatched, so /static/db/ACHIEVEMENT
	// fell through to {} and the achievement tab rendered empty and
	// froze on the completion-date sort for EVERY coach (user report
	// 2026-11-13). Serve the same catalogue at both paths.
	if c.Request.URL.Path == "/static/info/ACHIEVEMENT" || c.Request.URL.Path == "/static/db/ACHIEVEMENT" ||
		c.Request.URL.Path == "/static/info/achievement" || c.Request.URL.Path == "/static/db/achievement" {
		writeJSON(c, game.Achieve(s.d.GamedataDir).StaticAchievement())
		return
	}
	if hit := s.fromFile(c.Request.URL.Path); hit != nil {
		c.Data(http.StatusOK, "application/json", hit)
		return
	}
	writeJSON(c, map[string]any{})
}

// cdnFile ports hc.cdn_file over the local assets tree.
func (s *Server) cdnFile(c *gin.Context) {
	rel := strings.TrimPrefix(c.Request.URL.Path, "/cdn/")
	if strings.HasSuffix(rel, "_timestamp") || strings.HasSuffix(rel, "timestamp") {
		c.Data(http.StatusOK, "text/plain", []byte("1700000000"))
		return
	}
	roots := []string{s.d.AssetsDir, filepath.Join(s.d.AssetsDir, "externalAssets")}
	try := func(root, r string) (string, bool) {
		f := filepath.Join(root, filepath.Clean("/"+r))
		if !strings.HasPrefix(f, filepath.Clean(root)) {
			return "", false
		}
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
			return f, true
		}
		return "", false
	}
	found := ""
	for _, root := range roots {
		if f, ok := try(root, rel); ok {
			found = f
			break
		}
		if strings.HasPrefix(rel, "externalAssets/") {
			if f, ok := try(root, strings.TrimPrefix(rel, "externalAssets/")); ok {
				found = f
				break
			}
		}
	}
	if found == "" && strings.Contains(strings.ToLower(rel), "badge/") {
		sizes := []string{"small.png", "large.png", "normal.png"}
		if strings.Contains(strings.ToLower(rel), "large") {
			sizes = []string{"large.png", "small.png", "normal.png"}
		}
	outer:
		for _, root := range roots {
			for _, bid := range []string{"900001", "1001"} {
				for _, sz := range sizes {
					if f, ok := try(root, filepath.Join("externalAssets", "badge", bid, sz)); ok {
						found = f
						break outer
					}
				}
			}
		}
	}
	if found == "" {
		c.Status(http.StatusNotFound)
		return
	}
	ctype := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(found)) {
	case ".png":
		ctype = "image/png"
	case ".jpg":
		ctype = "image/jpeg"
	case ".swf":
		ctype = "application/x-shockwave-flash"
	case ".txt":
		ctype = "text/plain"
	}
	c.File(found)
	_ = ctype
}
