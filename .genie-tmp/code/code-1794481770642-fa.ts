const path = `${Deno.env.get("HOME")}/.genie/plugins`;
try {
  for await (const entry of Deno.readDir(path)) {
    console.log(`${entry.name} ${entry.isDirectory ? "dir" : "file"}`);
  }
} catch (e) {
  console.log("Path does not exist:", path, e);
}