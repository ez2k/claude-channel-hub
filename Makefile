.PHONY: build run dev clean add-skill docker-build docker-run

build:
	go build -o bin/claude-channel-hub ./cmd/bot

run: build
	./bin/claude-channel-hub -config configs/channels.yaml

dev:
	air -c .air.toml || go run ./cmd/bot -config configs/channels.yaml -data ./data

clean:
	rm -rf bin/

add-skill:
	@mkdir -p skills/$(NAME)
	@printf -- '---\nname: $(NAME)\ndescription: $(DESC)\ntags: []\n---\n\n# $(NAME) Skill\n\nAdd instructions here.\n' > skills/$(NAME)/SKILL.md
	@echo "✅ Created skills/$(NAME)/SKILL.md"

docker-build:
	docker build -t claude-channel-hub .

docker-run:
	docker run --env-file .env -p 8080:8080 -v $$(pwd)/data:/app/data claude-channel-hub
