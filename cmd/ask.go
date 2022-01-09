package cmd

import (
	"fmt"
	"github.com/eiannone/keyboard"
	"github.com/jedib0t/go-pretty/v6/text"
	"strings"
	"unicode"
)

func ask(question string, options map[rune]string, defaultOption rune) rune {
	optionsString := ""
	for key, option := range options {
		selector := "[" + string(key) + "]"
		if defaultOption == key {
			selector = strings.ToUpper(selector)
		}
		optionsString += strings.Replace(option, string(key), selector, 1) + " / "
	}
	optionsString = strings.Trim(optionsString, "/ ")

	fmt.Print(text.FgGreen.Sprintf("? "))
	fmt.Print(text.FgWhite.Sprintf("%v (%v) ", question, optionsString))

	for {
		char, key, err := keyboard.GetSingleKey()
		if err != nil || key == keyboard.KeyEnter {
			return defaultOption
		}

		lchar := unicode.ToLower(char)

		for option := range options {
			if option == lchar {
				fmt.Println(string(char))
				return option
			}
		}
	}
}
