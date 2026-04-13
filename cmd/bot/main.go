package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/your-org/claude-harness/internal/admin"
	"github.com/your-org/claude-harness/internal/bot"
	"github.com/your-org/claude-harness/internal/config"
	"github.com/your-org/claude-harness/internal/supervisor"
	"github.com/your-org/claude-harness/internal/version"
)

func main() {
	configPath := flag.String("config", "configs/channels.yaml", "Config YAML path")
	// TODO: dataDir used by store/memory/profile — re-enable when agent wiring is restored
	_ = flag.String("data", "./data", "Data directory")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║   🤖 Claude Harness v4.0                 ║")
	fmt.Println("║   Bot/Channel Orchestrator               ║")
	fmt.Println("╚══════════════════════════════════════════╝")

	// Load .env file (if exists)
	if err := config.LoadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		log.Printf("⚠️  .env: %v", err)
	}

	// Config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("❌ Config: %v", err)
	}

	// Version manager
	versionMgr := version.NewManager(cfg.Claude.VersionsDir, cfg.Claude.DefaultVersion)
	fmt.Printf("🔗 Claude Code: %s\n", versionMgr.Resolve("system"))

	// Supervisor
	sv := supervisor.New(supervisor.Config{
		HealthCheckInterval: cfg.Supervisor.HealthCheckInterval,
		MaxRestarts:         cfg.Supervisor.MaxRestarts,
		RestartDelay:        cfg.Supervisor.RestartDelay,
		RestartBackoffMax:   cfg.Supervisor.RestartBackoffMax,
	}, versionMgr)

	// Build bot objects from config and assign channels
	bots := buildBots(cfg)
	for _, b := range bots {
		sv.Register(b)
		fmt.Printf("🤖 [%s] Bot: type=%s plugin=%s channels=%d\n",
			b.Config.ID, b.Config.Type, b.Config.Plugin, len(b.Channels))
		for _, ch := range b.Channels {
			fmt.Printf("   📡 [%s] match=%s data=%s\n", ch.ID, ch.Match.Type, ch.DataDir)
		}
	}

	// Start
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n🛑 Shutting down...")
		sv.Stop()
		cancel()
	}()

	sv.Start(ctx)

	adminSrv := admin.NewServer(cfg.Admin.Addr, sv, nil)
	if err := adminSrv.Start(ctx); err != nil {
		log.Printf("Admin stopped: %v", err)
	}
}

// buildBots creates bot.Bot objects from config, assigning channels to their bots.
func buildBots(cfg *config.Root) []*bot.Bot {
	botMap := make(map[string]*bot.Bot)

	for _, bc := range cfg.Bots {
		b := &bot.Bot{
			Config: bot.BotConfig{
				ID:            bc.ID,
				Type:          bc.Type,
				Name:          bc.Name,
				Enabled:       bc.Enabled,
				Token:         bc.Token,
				Plugin:           bc.Plugin,
				PluginDir:        bc.PluginDir,
				PluginMarketplace: bc.PluginMarketplace,
				ClaudeVersion:    bc.ClaudeVersion,
				Model:         bc.Model,
				SystemPrompt:  bc.SystemPrompt,
			},
		}
		botMap[bc.ID] = b
	}

	// Assign channels to their bots
	for _, cc := range cfg.Channels {
		b, ok := botMap[cc.Bot]
		if !ok {
			log.Printf("⚠️  Channel [%s] references unknown bot [%s], skipping", cc.ID, cc.Bot)
			continue
		}
		b.Channels = append(b.Channels, bot.ChannelConfig{
			ID:           cc.ID,
			Bot:          cc.Bot,
			Name:         cc.Name,
			Match:        bot.MatchConfig{
				Type:     cc.Match.Type,
				ChatIDs:  cc.Match.ChatIDs,
				UserIDs:  cc.Match.UserIDs,
				TopicIDs: cc.Match.TopicIDs,
			},
			Model:        cc.Model,
			SystemPrompt: cc.SystemPrompt,
			DataDir:      cc.DataDir,
		})
	}

	var result []*bot.Bot
	for _, b := range botMap {
		result = append(result, b)
	}
	return result
}
