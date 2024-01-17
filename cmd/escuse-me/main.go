package main

import (
	"embed"
	"fmt"
	clay "github.com/go-go-golems/clay/pkg"
	"github.com/go-go-golems/clay/pkg/cmds"
	cli_cmds "github.com/go-go-golems/escuse-me/cmd/escuse-me/cmds"
	es_cmds "github.com/go-go-golems/escuse-me/pkg/cmds"
	"github.com/go-go-golems/escuse-me/pkg/cmds/layers"
	"github.com/go-go-golems/glazed/pkg/cli"
	glazed_cmds "github.com/go-go-golems/glazed/pkg/cmds"
	"github.com/go-go-golems/glazed/pkg/cmds/alias"
	"github.com/go-go-golems/glazed/pkg/cmds/loaders"
	"github.com/go-go-golems/glazed/pkg/help"
	"github.com/go-go-golems/glazed/pkg/helpers/cast"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"os"
)

var rootCmd = &cobra.Command{
	Use:   "escuse-me",
	Short: "GO GO GOLEM ESCUSE ME ELASTIC SEARCH GADGET",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// reinitialize the logger because we can now parse --log-level and co
		// from the command line flag
		err := clay.InitLogger()
		cobra.CheckErr(err)
	},
}

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "run-command" && os.Args[2] != "--help" {
		// load the command
		clientFactory := layers.NewESClientFromParsedLayers
		loader := es_cmds.NewElasticSearchCommandLoader(clientFactory)
		fi, err := os.Stat(os.Args[2])
		cobra.CheckErr(err)
		if !fi.IsDir() {
			fmt.Printf("Expected directory, got file")
			os.Exit(1)
		}

		path := os.Args[2]
		if path[0] != '/' {
			// resolve absolute path from .
			wd, err := os.Getwd()
			cobra.CheckErr(err)
			path = wd + "/" + path
		}

		esParameterLayer, err := layers.NewESParameterLayer()
		cobra.CheckErr(err)

		options := []glazed_cmds.CommandDescriptionOption{
			glazed_cmds.WithLayersList(esParameterLayer),
		}
		aliasOptions := []alias.Option{}
		fs := os.DirFS(path)
		cmds, err := loaders.LoadCommandsFromFS(
			fs, ".",
			loader,
			options, aliasOptions,
		)
		if err != nil {
			fmt.Printf("Could not load command: %v\n", err)
			os.Exit(1)
		}
		if len(cmds) != 1 {
			fmt.Printf("Expected exactly one command, got %d", len(cmds))
			os.Exit(1)
		}

		cobraCommand, err := es_cmds.BuildCobraCommandWithEscuseMeMiddlewares(cmds[0])
		if err != nil {
			fmt.Printf("Could not build cobra command: %v\n", err)
			os.Exit(1)
		}

		_, err = initRootCmd()
		cobra.CheckErr(err)

		rootCmd.AddCommand(cobraCommand)
		restArgs := os.Args[3:]
		os.Args = append([]string{os.Args[0], cobraCommand.Use}, restArgs...)
	} else {
		helpSystem, err := initRootCmd()
		cobra.CheckErr(err)

		err = initAllCommands(helpSystem)
		cobra.CheckErr(err)
	}

	err := rootCmd.Execute()
	cobra.CheckErr(err)
}

var runCommandCmd = &cobra.Command{
	Use:   "run-command",
	Short: "Run a command from a file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		panic(fmt.Errorf("not implemented"))
	},
}

//go:embed doc/*
var docFS embed.FS

//go:embed queries/*
var queriesFS embed.FS

func initRootCmd() (*help.HelpSystem, error) {
	helpSystem := help.NewHelpSystem()
	err := helpSystem.LoadSectionsFromFS(docFS, ".")
	if err != nil {
		panic(err)
	}

	helpSystem.SetupCobraRootCommand(rootCmd)

	err = clay.InitViper("escuse-me", rootCmd)
	if err != nil {
		panic(err)
	}
	err = clay.InitLogger()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error initializing logger: %s\n", err)
		os.Exit(1)
	}

	rootCmd.AddCommand(runCommandCmd)
	return helpSystem, nil

}

func initAllCommands(helpSystem *help.HelpSystem) error {
	repositories := viper.GetStringSlice("repositories")

	defaultDirectory := "$HOME/.escuse-me/queries"
	repositories = append(repositories, defaultDirectory)

	esParameterLayer, err := layers.NewESParameterLayer()
	if err != nil {
		return err
	}
	locations := cmds.NewCommandLocations(
		cmds.WithEmbeddedLocations(
			cmds.EmbeddedCommandLocation{
				FS:      queriesFS,
				Name:    "embed",
				Root:    "queries",
				DocRoot: "queries/doc",
			}),
		cmds.WithRepositories(repositories...),
		cmds.WithHelpSystem(helpSystem),
		cmds.WithAdditionalLayers(esParameterLayer),
	)

	clientFactory := layers.NewESClientFromParsedLayers
	loader := es_cmds.NewElasticSearchCommandLoader(clientFactory)

	commandLoader := cmds.NewCommandLoader[*es_cmds.ElasticSearchCommand](locations)
	commands, aliases, err := commandLoader.LoadCommands(loader, helpSystem)
	if err != nil {
		return err
	}

	commands_, ok := cast.CastList[glazed_cmds.Command](commands)
	if !ok {
		return fmt.Errorf("could not cast commands to GlazeCommand")
	}
	err = cli.AddCommandsToRootCommand(rootCmd, commands_, aliases,
		cli.WithCobraMiddlewaresFunc(es_cmds.GetCobraCommandEscuseMeMiddlewares),
	)
	if err != nil {
		return err
	}

	esCommands, ok := cast.CastList[*es_cmds.ElasticSearchCommand](commands)
	if !ok {
		return fmt.Errorf("could not cast commands to ElasticSearchCommand")
	}

	queriesCommand, err := es_cmds.NewQueriesCommand(esCommands, aliases)
	if err != nil {
		return err
	}
	cobraQueriesCommand, err := es_cmds.BuildCobraCommandWithEscuseMeMiddlewares(queriesCommand)
	if err != nil {
		return err
	}

	rootCmd.AddCommand(cobraQueriesCommand)

	infoCommand, err := cli_cmds.NewInfoCommand()
	if err != nil {
		return err
	}
	infoCmd, err := es_cmds.BuildCobraCommandWithEscuseMeMiddlewares(infoCommand)
	if err != nil {
		return err
	}
	rootCmd.AddCommand(infoCmd)

	serveCommand := cli_cmds.NewServeCommand(repositories,
		glazed_cmds.WithLayersList(esParameterLayer),
	)
	serveCmd, err := es_cmds.BuildCobraCommandWithEscuseMeMiddlewares(serveCommand)
	if err != nil {
		return err
	}
	rootCmd.AddCommand(serveCmd)

	indicesCommand := &cobra.Command{
		Use:   "indices",
		Short: "ES indices related commands",
	}
	rootCmd.AddCommand(indicesCommand)

	indicesListCommand, err := cli_cmds.NewIndicesListCommand()
	if err != nil {
		return err
	}
	indicesListCmd, err := es_cmds.BuildCobraCommandWithEscuseMeMiddlewares(indicesListCommand)
	if err != nil {
		return err
	}
	indicesCommand.AddCommand(indicesListCmd)

	indicesStatsCommand, err := cli_cmds.NewIndicesStatsCommand()
	if err != nil {
		return err
	}
	indicesStatsCmd, err := es_cmds.BuildCobraCommandWithEscuseMeMiddlewares(indicesStatsCommand)
	if err != nil {
		return err
	}
	indicesCommand.AddCommand(indicesStatsCmd)

	indicesGetMappingCommand, err := cli_cmds.NewIndicesGetMappingCommand()
	if err != nil {
		return err
	}
	indicesGetMappingCmd, err := es_cmds.BuildCobraCommandWithEscuseMeMiddlewares(indicesGetMappingCommand)
	if err != nil {
		return err
	}
	indicesCommand.AddCommand(indicesGetMappingCmd)

	return nil
}
