use crate::{Request, Response};
pub fn handle(req: &Request) -> Option<Response> {
    if req.method != "GET" && req.method != "HEAD" {
        return None;
    }
    let p = req.path().strip_prefix("/__admin/")?;
    let data: &[u8] = match p {
        "docs" => include_bytes!("../../internal/adminassets/doc-index.html"),
        "docs/swagger" => {
            include_bytes!("../../internal/adminassets/swagger/wiremock-admin-api.json")
        }
        "swagger-ui" | "swagger-ui/" => {
            include_bytes!("../../internal/adminassets/assets/swagger-ui/index.html")
        }
        "recorder" | "recorder/" => {
            include_bytes!("../../internal/adminassets/assets/recorder/index.html")
        }
        "recorder/images/wiremock-concept-icon-01.png" => include_bytes!(
            "../../internal/adminassets/assets/recorder/images/wiremock-concept-icon-01.png"
        ),
        "recorder/lib/jquery-4.0.0.min.js" => {
            include_bytes!("../../internal/adminassets/assets/recorder/lib/jquery-4.0.0.min.js")
        }
        "swagger-ui/swagger-ui-dist/swagger-ui-es-bundle-core.js.map" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-es-bundle-core.js.map"
        ),
        "swagger-ui/swagger-ui-dist/favicon-16x16.png" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/favicon-16x16.png"
        ),
        "swagger-ui/swagger-ui-dist/index.html" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/index.html"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-standalone-preset.js.LICENSE.txt" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-standalone-preset.js.LICENSE.txt"
        ),
        "swagger-ui/swagger-ui-dist/LICENSE" => {
            include_bytes!("../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/LICENSE")
        }
        "swagger-ui/swagger-ui-dist/swagger-ui.js" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui.js"
        ),
        "swagger-ui/swagger-ui-dist/oauth2-redirect.js" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/oauth2-redirect.js"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-es-bundle-core.js" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-es-bundle-core.js"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-standalone-preset.js.map" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-standalone-preset.js.map"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui.css" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui.css"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-bundle.js.LICENSE.txt" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-bundle.js.LICENSE.txt"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui.js.map" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui.js.map"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-bundle.js" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-bundle.js"
        ),
        "swagger-ui/swagger-ui-dist/index.js" => {
            include_bytes!("../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/index.js")
        }
        "swagger-ui/swagger-ui-dist/index.css" => {
            include_bytes!("../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/index.css")
        }
        "swagger-ui/swagger-ui-dist/swagger-ui-es-bundle-core.js.LICENSE.txt" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-es-bundle-core.js.LICENSE.txt"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-es-bundle.js.map" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-es-bundle.js.map"
        ),
        "swagger-ui/swagger-ui-dist/log.es-bundle-sizes.swagger-ui.txt" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/log.es-bundle-sizes.swagger-ui.txt"
        ),
        "swagger-ui/swagger-ui-dist/absolute-path.js" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/absolute-path.js"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui.css.map" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui.css.map"
        ),
        "swagger-ui/swagger-ui-dist/NOTICE" => {
            include_bytes!("../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/NOTICE")
        }
        "swagger-ui/swagger-ui-dist/README.md" => {
            include_bytes!("../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/README.md")
        }
        "swagger-ui/swagger-ui-dist/swagger-ui-standalone-preset.js" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-standalone-preset.js"
        ),
        "swagger-ui/swagger-ui-dist/package.json" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/package.json"
        ),
        "swagger-ui/swagger-ui-dist/swagger-initializer.js" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-initializer.js"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-es-bundle.js" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-es-bundle.js"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-bundle.js.map" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-bundle.js.map"
        ),
        "swagger-ui/swagger-ui-dist/log.bundle-sizes.swagger-ui.txt" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/log.bundle-sizes.swagger-ui.txt"
        ),
        "swagger-ui/swagger-ui-dist/log.es-bundle-core-sizes.swagger-ui.txt" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/log.es-bundle-core-sizes.swagger-ui.txt"
        ),
        "swagger-ui/swagger-ui-dist/oauth2-redirect.html" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/oauth2-redirect.html"
        ),
        "swagger-ui/swagger-ui-dist/swagger-ui-es-bundle.js.LICENSE.txt" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/swagger-ui-es-bundle.js.LICENSE.txt"
        ),
        "swagger-ui/swagger-ui-dist/favicon-32x32.png" => include_bytes!(
            "../../internal/adminassets/assets/swagger-ui/swagger-ui-dist/favicon-32x32.png"
        ),
        _ => return None,
    };
    Some(Response::new(
        200,
        if req.method == "HEAD" {
            vec![]
        } else {
            data.to_vec()
        },
    ))
}
