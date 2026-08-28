module asterion-core

go 1.25.0

require (
	asterion-lab v0.0.0-00010101000000-000000000000
	github.com/Tarafagat/asterion-language v0.0.0-00010101000000-000000000000
	github.com/Tarafagat/asterion-plugin-contract v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.10.2
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)

// asterion-lab, asterion-plugin-contract y asterion-language viven en su
// propio módulo cada uno, hermanos de este (carpetas al lado de
// asterion-core/) — ninguno está publicado en ningún registry, así que se
// referencian por ruta local, mismo criterio que asterion-shared del lado
// Python (editable install consumido por asterion-core/backend-core y
// asterion-cloud/backend).
replace asterion-lab => ../asterion-lab

replace github.com/Tarafagat/asterion-plugin-contract => ../asterion-plugin-contract

replace github.com/Tarafagat/asterion-language => ../asterion-language
