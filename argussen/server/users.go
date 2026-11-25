package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const userHelp = `Users:
  argus user list
  argus user add <name> <cameras|*> [--admin]   (cameras separated by commas, * = all)
  argus user cams <name> <cameras|*>
  argus user passwd <name>
  argus user logout <name>                       (logs out all devices)
  argus user del <name>`

func readPassword() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password: ")
		a, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		fmt.Fprint(os.Stderr, "Again: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(a) != string(b) {
			return "", fmt.Errorf("the two passwords do not match")
		}
		return checkLen(string(a))
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return checkLen(strings.TrimRight(line, "\r\n"))
}

func checkLen(p string) (string, error) {
	if len([]rune(p)) < 8 {
		return "", fmt.Errorf("the password must be at least 8 characters")
	}
	return p, nil
}

func userCmd(db *sql.DB, cfg *Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", userHelp)
	}
	validCams := func(s string) (string, error) {
		if s == "*" {
			return s, nil
		}
		var out []string
		for _, c := range strings.Split(s, ",") {
			c = strings.TrimSpace(c)
			found := false
			for _, k := range cams.All() {
				if k.ID == c {
					found = true
				}
			}
			if !found {
				return "", fmt.Errorf("unknown camera %q", c)
			}
			out = append(out, c)
		}
		return strings.Join(out, ","), nil
	}
	need := func(n int) error {
		if len(args) < n {
			return fmt.Errorf("%s", userHelp)
		}
		return nil
	}
	affected := func(r sql.Result, err error) error {
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return fmt.Errorf("the user does not exist")
		}
		return nil
	}
	switch args[0] {
	case "list":
		rows, err := db.Query(`SELECT u.id,u.name,u.admin,u.cams,(SELECT COUNT(*) FROM sessions s WHERE s.user_id=u.id) FROM users u ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, n int64
			var name, cams string
			var admin int
			rows.Scan(&id, &name, &admin, &cams, &n)
			a := ""
			if admin == 1 {
				a = " (admin)"
			}
			fmt.Printf("%-12s cameras: %-40s devices logged in: %d%s\n", name, cams, n, a)
		}
		return nil
	case "add":
		if err := need(3); err != nil {
			return err
		}
		cams, err := validCams(args[2])
		if err != nil {
			return err
		}
		pw, err := readPassword()
		if err != nil {
			return err
		}
		admin := 0
		if len(args) > 3 && args[3] == "--admin" {
			admin = 1
		}
		_, err = db.Exec(`INSERT INTO users(name,pw,admin,cams) VALUES(?,?,?,?)`, args[1], hashPassword(pw), admin, cams)
		if err == nil {
			fmt.Println("User", args[1], "created.")
		}
		return err
	case "cams":
		if err := need(3); err != nil {
			return err
		}
		cams, err := validCams(args[2])
		if err != nil {
			return err
		}
		return affected(db.Exec(`UPDATE users SET cams=? WHERE name=?`, cams, args[1]))
	case "passwd":
		if err := need(2); err != nil {
			return err
		}
		pw, err := readPassword()
		if err != nil {
			return err
		}
		if err := affected(db.Exec(`UPDATE users SET pw=? WHERE name=?`, hashPassword(pw), args[1])); err != nil {
			return err
		}
		fmt.Println("Password changed.")
		return nil
	case "logout":
		if err := need(2); err != nil {
			return err
		}
		_, err := db.Exec(`DELETE FROM sessions WHERE user_id=(SELECT id FROM users WHERE name=?)`, args[1])
		return err
	case "del":
		if err := need(2); err != nil {
			return err
		}
		db.Exec(`DELETE FROM sessions WHERE user_id=(SELECT id FROM users WHERE name=?)`, args[1])
		db.Exec(`DELETE FROM kv WHERE k='order:' || (SELECT id FROM users WHERE name=?)`, args[1]) // their own camera order
		return affected(db.Exec(`DELETE FROM users WHERE name=?`, args[1]))
	}
	return fmt.Errorf("%s", userHelp)
}
