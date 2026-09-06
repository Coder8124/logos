package main

import (
	"errors"
	"fmt"

	"github.com/Coder8124/brain/internal/selfupdate"
)

// updateCmd is `brain update` / `brain update --check`. It is the one place
// in this codebase that reaches the network on its own initiative, and only
// because a person typed this exact command — see selfupdate's package
// comment for the rest of that promise.
func updateCmd(args []string) error {
	if selfupdate.IsDevBuild(version) {
		return fmt.Errorf("this is an unstamped dev build; `brain update` has nothing to check it against")
	}

	if hasFlag(args, "--check") {
		rel, err := selfupdate.CheckOnly(version, selfupdate.Options{})
		if err != nil {
			return err
		}
		if rel.Version == version {
			fmt.Printf("brain %s is current\n", version)
			return nil
		}
		fmt.Printf("brain %s → %s available\n", version, rel.Version)
		return nil
	}

	res, err := selfupdate.Update(version, selfupdate.Options{})
	if err != nil {
		var se *selfupdate.Error
		if errors.As(err, &se) {
			return fmt.Errorf("%s: %w", se.Step, se.Err)
		}
		return err
	}

	if res.Asset == "" {
		fmt.Printf("brain %s is current\n", res.From)
		return nil
	}

	fmt.Printf("brain %s → %s\n", res.From, res.To)
	fmt.Printf("  downloaded  %s (%s)\n", res.Asset, humanSize(res.Size))
	fmt.Printf("  checksum    ok (sha256 %s…)\n", res.Checksum[:12])
	fmt.Printf("  verified    new binary reports %s\n", res.To)
	fmt.Printf("  replaced    %s\n", res.Replaced)

	if selfupdate.DetectInstall(res.Replaced) == selfupdate.NPMManaged {
		fmt.Println("\nthis install is managed by npm, so npm's own version metadata still says the old release.")
		fmt.Println("`npm i -g @noeton/logos@latest` keeps that in sync too.")
	}
	return nil
}

// humanSize renders a byte count the way a person reads a download size —
// distinct from the retired humanBytes, which formatted an index/vault size,
// not a network transfer.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
