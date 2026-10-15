use std::io::Write;

use anyhow::bail;
use clap::Args as ClapArgs;

#[derive(Debug, ClapArgs)]
pub struct Args {
    /// Who to greet.
    #[arg(long, default_value = "World")]
    pub name: String,
}

pub fn run(args: &Args, out: &mut impl Write) -> anyhow::Result<()> {
    let name = args.name.trim();
    if name.is_empty() {
        bail!("--name must not be empty");
    }
    writeln!(out, "Hello, {name}!")?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn greet(name: &str) -> anyhow::Result<String> {
        let mut out = Vec::new();
        run(
            &Args {
                name: name.to_owned(),
            },
            &mut out,
        )?;
        Ok(String::from_utf8(out)?)
    }

    #[test]
    fn greets_by_name() {
        assert_eq!(greet("Ada").unwrap(), "Hello, Ada!\n");
    }

    #[test]
    fn trims_the_name() {
        assert_eq!(greet("  Ada ").unwrap(), "Hello, Ada!\n");
    }

    #[test]
    fn blank_name_is_an_error() {
        assert!(greet("   ").is_err());
    }
}
