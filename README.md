## Output Mermaid graph script from bundle database

### Required:
make, sqlite3, go v14+, container execution environment (such as Docker, podman)

Way to create Mermaid graph image from script text, suggested method:

### Usage
Adjust the sqlite file pointed to in `sqlite3.sql`, this controls what index gets graphed

Then:
```bash
make run
```

Your Mermaid graph should open if `open` command opens PNG files on your host.

If not, image file saved as `/tmp/mermaid.mer.png`<br>
Mermaid script file will be `./mermaid.mer`

### Note
First time usage of `make run` may be slow due to download of Mermaid Docker image.