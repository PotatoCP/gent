// docs:start tools-script
const reply = tool.call({ tool: "receipt", args: {} });
tool.forward(reply.parts.filter(part => part.kind === "bytes"));
console.log("Receipt ready.");
// docs:end tools-script
