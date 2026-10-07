# clawtop

Kompaktowy monitor terminalowy w stylu htop: CPU (per rdzeń), pamięć, swap, dyski, load average oraz limity subskrypcji Claude (5h / 7d / Opus / Sonnet).

Limity Claude są pobierane z tego samego endpointu, którego używa `/usage` w Claude Code. Token OAuth jest czytany z `~/.claude/.credentials.json` (albo `$CLAUDE_CONFIG_DIR`) przy każdym odświeżeniu.

## Instalacja

```sh
go install github.com/krs2k/clawtop@latest
```

## Klawisze

- `q` / `Esc` / `Ctrl+C` – wyjście
- `r` – odśwież limity Claude
