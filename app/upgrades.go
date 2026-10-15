package app

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types/module"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"

	crossreftypes "github.com/crossref/crossrefd/x/crossref/types"
)

const (
	CrossrefV2UpgradeName = "crossref-v2"
	CrossrefV3UpgradeName = "crossref-v3"
	CrossrefV4UpgradeName = "crossref-v4"
)

func (app *App) setupUpgradeHandlers() {
	app.UpgradeKeeper.SetUpgradeHandler(CrossrefV4UpgradeName, func(ctx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
		current := fromVM[crossreftypes.ModuleName]
		if current > 4 {
			return nil, fmt.Errorf("cannot downgrade crossref to v4")
		}
		for version, migrate := range []func(context.Context) error{app.CrossrefKeeper.Migrate1To2, app.CrossrefKeeper.Migrate2To3, app.CrossrefKeeper.Migrate3To4} {
			if current < uint64(version+2) {
				if err := migrate(ctx); err != nil {
					return nil, err
				}
			}
		}
		next := make(module.VersionMap, len(fromVM)+1)
		for name, version := range fromVM {
			next[name] = version
		}
		next[crossreftypes.ModuleName] = 4
		return next, nil
	})
	app.UpgradeKeeper.SetUpgradeHandler(
		CrossrefV2UpgradeName,
		func(ctx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
			if fromVM[crossreftypes.ModuleName] < 2 {
				if err := app.CrossrefKeeper.Migrate1To2(ctx); err != nil {
					return nil, err
				}
			}

			toVM := make(module.VersionMap, len(fromVM)+1)
			for moduleName, version := range fromVM {
				toVM[moduleName] = version
			}
			toVM[crossreftypes.ModuleName] = 2
			return toVM, nil
		},
	)
	app.UpgradeKeeper.SetUpgradeHandler(
		CrossrefV3UpgradeName,
		func(ctx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
			currentVersion := fromVM[crossreftypes.ModuleName]
			if currentVersion < 2 {
				if err := app.CrossrefKeeper.Migrate1To2(ctx); err != nil {
					return nil, err
				}
			}
			if currentVersion < 3 {
				if err := app.CrossrefKeeper.Migrate2To3(ctx); err != nil {
					return nil, err
				}
			}

			toVM := make(module.VersionMap, len(fromVM)+1)
			for moduleName, version := range fromVM {
				toVM[moduleName] = version
			}
			toVM[crossreftypes.ModuleName] = 3
			return toVM, nil
		},
	)
}
