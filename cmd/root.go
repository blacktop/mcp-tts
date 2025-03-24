/*
Copyright © 2025 blacktop

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"log"
	"net/http"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "mcp-say",
	Short: "TTS (text-to-speech) MCP Server",
	Long: `mcp-say is a TTS (text-to-speech) MCP Server.

It is a simple HTTP server that listens for requests on port 8080 and responds with a TTS stream.

It is designed to be used with the MCP protocol.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	Run: func(cmd *cobra.Command, args []string) {
		http.HandleFunc("/speak", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			// Get text from request
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Failed to parse form", http.StatusBadRequest)
				return
			}
			text := r.FormValue("text")
			if text == "" {
				http.Error(w, "Text is required", http.StatusBadRequest)
				return
			}

			// Execute the say command
			cmd := exec.Command("/usr/bin/say", "--rate=200", text)
			if err := cmd.Start(); err != nil {
				log.Printf("Failed to start say command: %v", err)
				http.Error(w, "Failed to process speech", http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Speaking: " + text))
		})

		log.Println("Starting MCP server on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.mcp-say.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
