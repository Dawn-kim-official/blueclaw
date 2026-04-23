import adapter from "@sveltejs/adapter-static";

const configuration = {
  kit: {
    adapter: adapter({
      pages: "../web/admin",
      assets: "../web/admin",
      fallback: "index.html"
    })
  }
};

export default configuration;
