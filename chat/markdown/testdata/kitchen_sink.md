## How to read a file

You can use `os.ReadFile`:

```go
data, err := os.ReadFile("config.json")
if err != nil {
  return err
}
```

A few things to note:

- It reads the **whole** file into memory.
- For large files, use a *streaming* reader instead.
  - `bufio.Scanner` is the usual choice.
- See the [package docs](https://pkg.go.dev/os "os on pkg.go.dev") for details.

> Note: `os.ReadFile` returns `[]byte`, not a string.
> Convert it with `string(data)` if you need text.

---

That's it. See <https://go.dev/> for more, and here is the mascot:
![the Go gopher](https://go.dev/gopher.png)
