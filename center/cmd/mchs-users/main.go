// Команда mchs-users — заведение учётных записей операторов тренажёра.
//
// Пароли нигде не хранятся в открытом виде, поэтому файл учётных
// записей не пишут руками: хеш считает эта утилита.
//
//	go run ./cmd/mchs-users -login petrov -name "Пётр Петров" -role operator
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ArnurKabdylkak/lss/center/internal/app/auth"
)

func main() {
	var (
		path     = flag.String("file", "users.json", "файл учётных записей")
		login    = flag.String("login", "", "логин оператора")
		name     = flag.String("name", "", "имя для журнала действий")
		role     = flag.String("role", string(auth.RoleOperator), "роль: operator или viewer")
		password = flag.String("password", "", "пароль (пусто — спросить)")
		list     = flag.Bool("list", false, "показать учётные записи")
		remove   = flag.String("remove", "", "удалить учётную запись")
	)
	flag.Parse()

	if err := run(*path, *login, *name, *role, *password, *list, *remove); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run(path, login, name, role, password string, list bool, remove string) error {
	users, err := load(path)
	if err != nil {
		return err
	}

	switch {
	case list:
		if len(users) == 0 {
			fmt.Println("учётных записей нет")
			return nil
		}
		for _, u := range users {
			fmt.Printf("%-16s %-24s %s\n", u.Login, u.Name, u.Role)
		}
		return nil

	case remove != "":
		kept := users[:0]
		found := false
		for _, u := range users {
			if u.Login == strings.ToLower(remove) {
				found = true
				continue
			}
			kept = append(kept, u)
		}
		if !found {
			return fmt.Errorf("учётная запись %q не найдена", remove)
		}
		if err := save(path, kept); err != nil {
			return err
		}
		fmt.Printf("учётная запись %q удалена\n", remove)
		return nil
	}

	if login == "" {
		return errors.New("нужен -login (или -list / -remove)")
	}
	if role != string(auth.RoleOperator) && role != string(auth.RoleViewer) {
		return fmt.Errorf("роль должна быть %q или %q", auth.RoleOperator, auth.RoleViewer)
	}

	if password == "" {
		password, err = ask()
		if err != nil {
			return err
		}
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	login = strings.ToLower(strings.TrimSpace(login))
	if name == "" {
		name = login
	}

	// Повторное заведение того же логина меняет пароль и роль —
	// это и есть смена пароля.
	replaced := false
	for i := range users {
		if users[i].Login == login {
			users[i] = auth.User{Login: login, Name: name, Role: auth.Role(role), Hash: hash}
			replaced = true
			break
		}
	}
	if !replaced {
		users = append(users, auth.User{Login: login, Name: name, Role: auth.Role(role), Hash: hash})
	}

	// Проверяем файл целиком до записи: иначе можно записать набор,
	// с которым центр потом не запустится.
	if _, err := auth.New(users); err != nil {
		return err
	}
	if err := save(path, users); err != nil {
		return err
	}

	if replaced {
		fmt.Printf("пароль и роль %q обновлены (%s)\n", login, role)
	} else {
		fmt.Printf("заведена учётная запись %q (%s)\n", login, role)
	}
	return nil
}

func load(path string) ([]auth.User, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var users []auth.User
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("разбор %s: %w", path, err)
	}
	return users, nil
}

func save(path string, users []auth.User) error {
	raw, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	// Права 0600: файл с хешами паролей не для всех.
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func ask() (string, error) {
	fmt.Print("пароль (не короче 8 символов): ")

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
